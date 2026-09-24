package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	WorkflowBatchJob     = "janus.batch-job"
	WorkflowBatchItem    = "janus.batch-item"
	ActivityExecuteTool  = "janus.execute-tool"
	SignalOperatorPrefix = "janus.operator-resolution."
)

type ToolDispatcher interface {
	Invoke(ctx context.Context, req ToolInvocationRequest) (ToolInvocationResult, error)
}

type Activities struct {
	Dispatcher ToolDispatcher
}

func RegisterTemporalComponents(w worker.Worker, activities *Activities) {
	w.RegisterWorkflowWithOptions(BatchJobWorkflow, workflow.RegisterOptions{Name: WorkflowBatchJob})
	w.RegisterWorkflowWithOptions(BatchItemWorkflow, workflow.RegisterOptions{Name: WorkflowBatchItem})
	w.RegisterActivityWithOptions(activities.ExecuteTool, activity.RegisterOptions{Name: ActivityExecuteTool})
}

func (a *Activities) ExecuteTool(ctx context.Context, req ToolInvocationRequest) (ToolInvocationResult, error) {
	start := time.Now().UTC()
	if a == nil || a.Dispatcher == nil {
		return ToolInvocationResult{
			Tool:         req.Tool,
			Success:      false,
			FailureClass: FailureClassInfrastructure,
			ErrorCode:    "dispatcher_unavailable",
			ErrorMessage: "no tool dispatcher registered for Temporal activity worker",
			StartedAt:    start,
			FinishedAt:   time.Now().UTC(),
		}, nil
	}
	result, err := a.Dispatcher.Invoke(ctx, req)
	if result.Tool.Name == "" {
		result.Tool = req.Tool
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = start
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	if err != nil {
		result.Success = false
		if result.FailureClass == "" {
			result.FailureClass = FailureClassInfrastructure
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = err.Error()
		}
	}
	return result, nil
}

type BatchJobWorkflowInput struct {
	Job     BatchJobRequest         `json:"job"`
	Catalog map[string]ToolContract `json:"catalog,omitempty"`
}

type batchItemWorkflowInput struct {
	Job      BatchJobRequest `json:"job"`
	Item     BatchItem       `json:"item"`
	Contract ToolContract    `json:"contract"`
}

func BatchJobWorkflow(ctx workflow.Context, input BatchJobWorkflowInput) (*BatchJobResult, error) {
	if err := input.Job.Validate(); err != nil {
		return nil, err
	}

	result := &BatchJobResult{
		BatchID:    input.Job.BatchID,
		TotalItems: len(input.Job.Items),
		Results:    make([]BatchItemResult, 0, len(input.Job.Items)),
	}

	parallelism := input.Job.MaxParallelism
	if parallelism <= 0 || parallelism > len(input.Job.Items) {
		parallelism = len(input.Job.Items)
	}

	for start := 0; start < len(input.Job.Items); start += parallelism {
		end := start + parallelism
		if end > len(input.Job.Items) {
			end = len(input.Job.Items)
		}

		futures := make([]workflow.ChildWorkflowFuture, 0, end-start)
		for _, item := range input.Job.Items[start:end] {
			contract := input.Catalog[item.Tool.CanonicalName()]
			if contract.Name == "" {
				contract = input.Catalog[item.Tool.Name]
			}
			childInput := batchItemWorkflowInput{
				Job:      input.Job,
				Item:     item,
				Contract: contract,
			}
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID: fmt.Sprintf("%s-%s", input.Job.BatchID, item.ItemID),
			})
			futures = append(futures, workflow.ExecuteChildWorkflow(childCtx, WorkflowBatchItem, childInput))
		}

		for offset, future := range futures {
			var childResult BatchItemResult
			if err := future.Get(ctx, &childResult); err != nil {
				item := input.Job.Items[start+offset]
				childResult = BatchItemResult{
					ItemID:       item.ItemID,
					Status:       BatchItemStatusFailed,
					FinalTool:    item.Tool,
					FailureClass: FailureClassInfrastructure,
					Error:        err.Error(),
				}
			}
			result.Results = append(result.Results, childResult)
		}
	}

	for _, itemResult := range result.Results {
		switch itemResult.Status {
		case BatchItemStatusComplete, BatchItemStatusCompensated:
			result.CompletedItems++
		case BatchItemStatusAwaitingOperator:
			result.AwaitingOperator++
		default:
			result.FailedItems++
		}
	}
	result.Status = AggregateBatchStatus(result.Results)
	return result, nil
}

func BatchItemWorkflow(ctx workflow.Context, input batchItemWorkflowInput) (*BatchItemResult, error) {
	contract := input.Contract
	if contract.Name == "" {
		contract = ToolContract{
			Name:          input.Item.Tool.Name,
			Version:       input.Item.Tool.Version,
			Description:   "ad hoc batch item contract",
			Deterministic: true,
			Idempotent:    true,
			InputSchema:   ToolSchema{Type: "object"},
			OutputSchema:  ToolSchema{Type: "object"},
			RetryPolicy:   DefaultRetryPolicy(),
			TimeoutPolicy: DefaultTimeoutPolicy(),
		}
	}
	if err := contract.Validate(); err != nil {
		return nil, err
	}

	retryPolicy := MergeRetryPolicy(input.Job.DefaultRetryPolicy, &contract.RetryPolicy)
	retryPolicy = MergeRetryPolicy(retryPolicy, input.Item.Retry)
	fallbacks := append(append([]FallbackEdge(nil), contract.Fallbacks...), input.Item.Fallbacks...)
	currentTool := input.Item.Tool
	if currentTool.Name == "" {
		currentTool = contract.Reference()
	}

	result := &BatchItemResult{
		ItemID:    input.Item.ItemID,
		Status:    BatchItemStatusFailed,
		FinalTool: currentTool,
	}
	usedTools := map[string]struct{}{currentTool.CanonicalName(): {}}
	workflowStart := workflow.Now(ctx)

toolLoop:
	for {
		for attempt := 1; ; attempt++ {
			req := ToolInvocationRequest{
				WorkflowID:     workflow.GetInfo(ctx).WorkflowExecution.ID,
				RunID:          workflow.GetInfo(ctx).WorkflowExecution.RunID,
				BatchID:        input.Job.BatchID,
				ItemID:         input.Item.ItemID,
				TenantID:       input.Job.TenantID,
				ActorID:        input.Job.ActorID,
				Tool:           currentTool,
				IdempotencyKey: buildIdempotencyKey(input.Job.BatchID, input.Item, currentTool, attempt),
				ExecutionMode:  resolveExecutionMode(contract, input.Job.HIPAAStrict),
				RequireAudit:   input.Job.RequireAuditTrail || contract.RequiresAudit,
				Attempt:        attempt,
				Payload:        input.Item.Input,
			}

			var invocation ToolInvocationResult
			err := workflow.ExecuteActivity(
				workflow.WithActivityOptions(ctx, activityOptions(contract.TimeoutPolicy)),
				ActivityExecuteTool,
				req,
			).Get(ctx, &invocation)
			if err != nil {
				invocation = ToolInvocationResult{
					Tool:         currentTool,
					Success:      false,
					FailureClass: FailureClassInfrastructure,
					ErrorCode:    "activity_execution_failed",
					ErrorMessage: err.Error(),
				}
			}

			result.Attempts = append(result.Attempts, AttemptRecord{
				Attempt:      attempt,
				Tool:         currentTool,
				Success:      invocation.Success,
				FailureClass: invocation.FailureClass,
				Summary:      invocation.Summary,
				ErrorMessage: invocation.ErrorMessage,
				Artifacts:    invocation.Artifacts,
				StartedAt:    invocation.StartedAt,
				FinishedAt:   invocation.FinishedAt,
			})
			result.Artifacts = append(result.Artifacts, invocation.Artifacts...)

			if invocation.Success {
				result.Status = BatchItemStatusComplete
				result.FinalTool = currentTool
				result.Output = invocation.Output
				result.Error = ""
				result.FailureClass = ""
				return result, nil
			}

			result.FinalTool = currentTool
			result.FailureClass = invocation.FailureClass
			result.Error = invocation.ErrorMessage

			elapsed := workflow.Now(ctx).Sub(workflowStart)
			if retryPolicy.CanRetry(attempt, elapsed, invocation.FailureClass) {
				delay := retryPolicy.BackoffDelay(
					attempt,
					fmt.Sprintf("%s:%s:%s", input.Job.BatchID, input.Item.ItemID, currentTool.CanonicalName()),
				)
				if err := workflow.Sleep(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}

			nextTool, ok := SelectFallback(fallbacks, invocation.FailureClass, usedTools)
			if ok {
				currentTool = nextTool
				usedTools[currentTool.CanonicalName()] = struct{}{}
				result.FinalTool = currentTool
				continue toolLoop
			}

			if input.Job.RequireManualResolution {
				resolution, received, waitErr := waitForOperatorResolution(ctx, input.Item.ItemID, input.Job.OperatorTimeout)
				if waitErr != nil {
					return nil, waitErr
				}
				if received {
					result.Resolution = &resolution
					switch resolution.Action {
					case ResolutionActionRetry:
						if len(resolution.InputOverride) > 0 {
							input.Item.Input = resolution.InputOverride
						}
						if resolution.ToolOverride.Name != "" {
							currentTool = resolution.ToolOverride
							usedTools[currentTool.CanonicalName()] = struct{}{}
						}
						workflowStart = workflow.Now(ctx)
						continue toolLoop
					case ResolutionActionOverrideOutput:
						result.Status = BatchItemStatusComplete
						result.Output = resolution.OutputOverride
						result.Error = ""
						result.FailureClass = ""
						if resolution.ToolOverride.Name != "" {
							result.FinalTool = resolution.ToolOverride
						}
						return result, nil
					case ResolutionActionCompensate:
						compensated, compErr := runCompensation(ctx, input, result)
						if compErr != nil {
							return nil, compErr
						}
						return compensated, nil
					case ResolutionActionFail:
						result.Status = BatchItemStatusFailed
						return result, nil
					}
				}
				result.Status = BatchItemStatusAwaitingOperator
				return result, nil
			}

			if input.Item.Compensation != nil {
				compensated, compErr := runCompensation(ctx, input, result)
				if compErr != nil {
					return nil, compErr
				}
				return compensated, nil
			}
			return result, nil
		}
	}
}

func activityOptions(timeout TimeoutPolicy) workflow.ActivityOptions {
	timeout = normalizeTimeouts(timeout)
	return workflow.ActivityOptions{
		ScheduleToCloseTimeout: timeout.ScheduleToClose,
		StartToCloseTimeout:    timeout.StartToClose,
		HeartbeatTimeout:       timeout.Heartbeat,
		RetryPolicy:            nil,
	}
}

func normalizeTimeouts(timeout TimeoutPolicy) TimeoutPolicy {
	defaults := DefaultTimeoutPolicy()
	if timeout.ScheduleToClose == 0 {
		timeout.ScheduleToClose = defaults.ScheduleToClose
	}
	if timeout.StartToClose == 0 {
		timeout.StartToClose = defaults.StartToClose
	}
	return timeout
}

func buildIdempotencyKey(batchID string, item BatchItem, tool ToolReference, attempt int) string {
	if item.IdempotencyKey != "" {
		return item.IdempotencyKey
	}
	return fmt.Sprintf("%s:%s:%s:%d", batchID, item.ItemID, tool.CanonicalName(), attempt)
}

func resolveExecutionMode(contract ToolContract, hipaaStrict bool) ExecutionMode {
	if hipaaStrict {
		return ExecutionModeLocalOnly
	}
	if contract.ExecutionMode != "" {
		return contract.ExecutionMode
	}
	return ExecutionModeLocalPreferred
}

func waitForOperatorResolution(ctx workflow.Context, itemID string, timeout time.Duration) (OperatorResolution, bool, error) {
	signalName := SignalOperatorPrefix + itemID
	channel := workflow.GetSignalChannel(ctx, signalName)
	if timeout <= 0 {
		var resolution OperatorResolution
		channel.Receive(ctx, &resolution)
		return resolution, true, nil
	}

	var (
		resolution OperatorResolution
		received   bool
	)
	timer := workflow.NewTimer(ctx, timeout)
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(channel, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &resolution)
		received = true
	})
	selector.AddFuture(timer, func(workflow.Future) {})
	selector.Select(ctx)
	return resolution, received, nil
}

func runCompensation(ctx workflow.Context, input batchItemWorkflowInput, result *BatchItemResult) (*BatchItemResult, error) {
	if input.Item.Compensation == nil {
		return result, nil
	}

	payload := input.Item.Compensation.Input
	if len(payload) == 0 {
		body, err := json.Marshal(map[string]any{
			"failed_item_id": input.Item.ItemID,
			"failed_tool":    result.FinalTool.CanonicalName(),
			"error":          result.Error,
		})
		if err != nil {
			return nil, err
		}
		payload = body
	}

	req := ToolInvocationRequest{
		WorkflowID:     workflow.GetInfo(ctx).WorkflowExecution.ID,
		RunID:          workflow.GetInfo(ctx).WorkflowExecution.RunID,
		BatchID:        input.Job.BatchID,
		ItemID:         input.Item.ItemID,
		TenantID:       input.Job.TenantID,
		ActorID:        input.Job.ActorID,
		Tool:           input.Item.Compensation.Tool,
		IdempotencyKey: buildIdempotencyKey(input.Job.BatchID, input.Item, input.Item.Compensation.Tool, 1) + ":compensate",
		ExecutionMode:  ExecutionModeLocalOnly,
		RequireAudit:   true,
		Attempt:        1,
		Payload:        payload,
	}

	var compensation ToolInvocationResult
	if err := workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, activityOptions(DefaultTimeoutPolicy())),
		ActivityExecuteTool,
		req,
	).Get(ctx, &compensation); err != nil {
		return nil, err
	}

	result.Attempts = append(result.Attempts, AttemptRecord{
		Attempt:      1,
		Tool:         input.Item.Compensation.Tool,
		Success:      compensation.Success,
		FailureClass: compensation.FailureClass,
		Summary:      compensation.Summary,
		ErrorMessage: compensation.ErrorMessage,
		Artifacts:    compensation.Artifacts,
		StartedAt:    compensation.StartedAt,
		FinishedAt:   compensation.FinishedAt,
	})
	if compensation.Success {
		result.Status = BatchItemStatusCompensated
		result.Artifacts = append(result.Artifacts, compensation.Artifacts...)
		return result, nil
	}
	result.Status = BatchItemStatusFailed
	result.Error = fmt.Sprintf("%s; compensation failed: %s", result.Error, compensation.ErrorMessage)
	return result, nil
}

