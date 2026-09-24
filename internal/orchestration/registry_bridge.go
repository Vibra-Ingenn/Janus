package orchestration

import (
	"sort"

	"janus/internal/tools"
)

type DraftContractOptions struct {
	Version       string
	Category      string
	Tags          []string
	ExecutionMode ExecutionMode
	RequiresAudit bool
}

func DraftContractFromToolDef(def tools.ToolDef, opts DraftContractOptions) ToolContract {
	inputProps := make(map[string]SchemaField, len(def.Parameters))
	required := make([]string, 0, len(def.Parameters))
	keys := make([]string, 0, len(def.Parameters))
	for key := range def.Parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		inputProps[key] = SchemaField{
			Type:        "string",
			Description: def.Parameters[key],
		}
		required = append(required, key)
	}

	return ToolContract{
		Name:          def.Name,
		Version:       opts.Version,
		Category:      opts.Category,
		Description:   def.Description,
		Tags:          append([]string(nil), opts.Tags...),
		Deterministic: true,
		Idempotent:    true,
		RequiresAudit: opts.RequiresAudit,
		ExecutionMode: opts.ExecutionMode,
		InputSchema: ToolSchema{
			Type:                 "object",
			Description:          "Draft schema generated from the Janus tool registry",
			Properties:           inputProps,
			Required:             required,
			AdditionalProperties: false,
		},
		OutputSchema: ToolSchema{
			Type:        "object",
			Description: "Standard Janus tool result envelope",
			Properties: map[string]SchemaField{
				"success": {Type: "boolean", Description: "True when the tool completed successfully"},
				"output":  {Type: "string", Description: "Human-readable or JSON-encoded output"},
				"error":   {Type: "string", Description: "Failure detail when success is false"},
			},
			Required:             []string{"success"},
			AdditionalProperties: false,
		},
		TimeoutPolicy: DefaultTimeoutPolicy(),
		RetryPolicy:   DefaultRetryPolicy(),
	}
}

