package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ExecResult is the output of a single shell command.
type ExecResult struct {
	Command  string        `json:"command"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration_ms"`
}

// Executor runs shell commands safely inside WSL2.
// Every command is checked against the blacklist before execution.
// Commands run with a hard timeout and inside a sandboxed working directory.
type Executor struct {
	timeout    time.Duration
	workingDir string // WSL2 path, e.g. /tmp/janus-work
}

// DefaultTimeout is the hard limit for any single command.
const DefaultTimeout = 60 * time.Second

// NewExecutor creates an Executor.
//
//	timeout     — hard wall-clock limit per command (0 = DefaultTimeout)
//	workingDir  — WSL2 working directory; use "" for /tmp/janus-work
func NewExecutor(timeout time.Duration, workingDir string) *Executor {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if workingDir == "" {
		workingDir = "/tmp/janus-work"
	}
	return &Executor{
		timeout:    timeout,
		workingDir: workingDir,
	}
}

// Run executes cmd inside WSL2 after passing it through the blacklist.
//
// The command runs as:
//
//	wsl.exe -- bash -c "mkdir -p <workingDir> && cd <workingDir> && ulimit -t 30 -v 2097152; <cmd>"
//
// Resource limits applied inside the WSL2 shell:
//   - ulimit -t 30        CPU time limit: 30 seconds
//   - ulimit -v 2097152   Virtual memory: 2 GB max
//
// The outer context deadline (ctx) provides the wall-clock timeout.
func (e *Executor) Run(ctx context.Context, cmd string) (ExecResult, error) {
	// Blacklist check — must pass before we touch any OS resource
	if err := CheckCommand(cmd); err != nil {
		return ExecResult{Command: cmd, ExitCode: -1}, err
	}

	// Compose the WSL2 invocation with resource limits
	inner := fmt.Sprintf(
		"mkdir -p %s && cd %s && ulimit -t 30 -v 2097152 2>/dev/null; %s",
		e.workingDir,
		e.workingDir,
		cmd,
	)

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	wslCmd := exec.CommandContext(ctx, "wsl.exe", "--", "bash", "-c", inner)

	var stdout, stderr bytes.Buffer
	wslCmd.Stdout = &stdout
	wslCmd.Stderr = &stderr

	start := time.Now()
	err := wslCmd.Run()
	dur := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ExecResult{
				Command:  cmd,
				Stderr:   err.Error(),
				ExitCode: -1,
				Duration: dur,
			}, fmt.Errorf("shell/executor: wsl.exe error: %w", err)
		}
	}

	return ExecResult{
		Command:  cmd,
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: exitCode,
		Duration: dur,
	}, nil
}

// RunMulti executes a slice of commands in sequence, stopping on first error
// with a non-zero exit code. Returns all results collected so far.
func (e *Executor) RunMulti(ctx context.Context, cmds []string) ([]ExecResult, error) {
	results := make([]ExecResult, 0, len(cmds))
	for _, cmd := range cmds {
		r, err := e.Run(ctx, cmd)
		results = append(results, r)
		if err != nil {
			return results, err
		}
		if r.ExitCode != 0 {
			return results, fmt.Errorf("shell/executor: command %q exited %d: %s", cmd, r.ExitCode, r.Stderr)
		}
	}
	return results, nil
}
