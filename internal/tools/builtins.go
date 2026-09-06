package tools

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// registerBuiltins adds the default set of local tools.
func (r *Registry) registerBuiltins() {
	r.Register(ToolDef{
		Name:        "read_file",
		Description: "Read the contents of a local file",
		Parameters:  map[string]string{"path": "File path to read"},
	}, toolReadFile)

	r.Register(ToolDef{
		Name:        "write_file",
		Description: "Write content to a local file (creates or overwrites)",
		Parameters:  map[string]string{"path": "File path to write", "content": "Content to write"},
	}, toolWriteFile)

	r.Register(ToolDef{
		Name:        "list_dir",
		Description: "List files and directories in a path",
		Parameters:  map[string]string{"path": "Directory path to list"},
	}, toolListDir)

	r.Register(ToolDef{
		Name:        "calculate",
		Description: "Evaluate a simple math expression (add, subtract, multiply, divide, power)",
		Parameters:  map[string]string{"expression": "Math expression like '2 + 3' or '10 * 5'"},
	}, toolCalculate)

	r.Register(ToolDef{
		Name:        "get_time",
		Description: "Get the current date and time",
		Parameters:  map[string]string{},
	}, toolGetTime)

	r.Register(ToolDef{
		Name:        "search_files",
		Description: "Search for files matching a glob pattern in a directory",
		Parameters:  map[string]string{"directory": "Base directory to search", "pattern": "Glob pattern (e.g. *.go, *.py)"},
	}, toolSearchFiles)

	r.Register(ToolDef{
		Name:        "run_command",
		Description: "Execute a shell command and return combined stdout+stderr. Use to run scripts, compile code, install packages, test programs, etc. Commands run relative to working_dir if provided.",
		Parameters: map[string]string{
			"command":     "Shell command to execute (e.g. 'python script.py', 'go build .', 'pip install requests')",
			"working_dir": "Directory to run the command in (optional)",
		},
	}, toolRunCommand)

	r.Register(ToolDef{
		Name:        "create_dir",
		Description: "Create a directory (and any parent directories) at the given path",
		Parameters:  map[string]string{"path": "Directory path to create"},
	}, toolCreateDir)

	r.Register(ToolDef{
		Name:        "delete",
		Description: "Delete a file or empty directory at the given path",
		Parameters:  map[string]string{"path": "Path to delete"},
	}, toolDelete)

	r.Register(ToolDef{
		Name:        "done",
		Description: "Signal that the task is complete. Provide a summary of what was accomplished.",
		Parameters:  map[string]string{"summary": "Brief summary of what was done"},
	}, toolDone)
}

func toolReadFile(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolResult{ToolName: "read_file", Success: false, Error: "path must be a string"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{ToolName: "read_file", Success: false, Error: err.Error()}
	}
	content := string(data)
	if len(content) > 4096 {
		content = content[:4096] + "\n... (truncated)"
	}
	return ToolResult{ToolName: "read_file", Success: true, Output: content}
}

func toolWriteFile(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolResult{ToolName: "write_file", Success: false, Error: "path must be a string"}
	}
	content, ok := args["content"].(string)
	if !ok {
		return ToolResult{ToolName: "write_file", Success: false, Error: "content must be a string"}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{ToolName: "write_file", Success: false, Error: err.Error()}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return ToolResult{ToolName: "write_file", Success: false, Error: err.Error()}
	}
	return ToolResult{ToolName: "write_file", Success: true, Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}
}

func toolListDir(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ToolResult{ToolName: "list_dir", Success: false, Error: err.Error()}
	}
	var sb strings.Builder
	for _, e := range entries {
		prefix := "  "
		if e.IsDir() {
			prefix = "D "
		}
		sb.WriteString(fmt.Sprintf("%s%s\n", prefix, e.Name()))
	}
	return ToolResult{ToolName: "list_dir", Success: true, Output: sb.String()}
}

func toolCalculate(args map[string]any) ToolResult {
	expr, ok := args["expression"].(string)
	if !ok || expr == "" {
		return ToolResult{ToolName: "calculate", Success: false, Error: "expression must be a string"}
	}
	// Simple two-operand calculator
	expr = strings.TrimSpace(expr)
	var a, b float64
	var op string
	for _, candidate := range []string{" + ", " - ", " * ", " / ", " ^ ", " ** "} {
		if idx := strings.Index(expr, candidate); idx >= 0 {
			op = strings.TrimSpace(candidate)
			fmt.Sscanf(expr[:idx], "%f", &a)
			fmt.Sscanf(expr[idx+len(candidate):], "%f", &b)
			break
		}
	}
	if op == "" {
		return ToolResult{ToolName: "calculate", Success: false, Error: "unsupported expression format"}
	}
	var result float64
	switch op {
	case "+":
		result = a + b
	case "-":
		result = a - b
	case "*":
		result = a * b
	case "/":
		if b == 0 {
			return ToolResult{ToolName: "calculate", Success: false, Error: "division by zero"}
		}
		result = a / b
	case "^", "**":
		result = math.Pow(a, b)
	}
	return ToolResult{ToolName: "calculate", Success: true, Output: fmt.Sprintf("%g", result)}
}

func toolGetTime(args map[string]any) ToolResult {
	_ = args
	return ToolResult{ToolName: "get_time", Success: true, Output: time.Now().Format(time.RFC3339)}
}

func toolSearchFiles(args map[string]any) ToolResult {
	dir, ok := args["directory"].(string)
	if !ok {
		dir = "."
	}
	if dir == "" {
		dir = "."
	}
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return ToolResult{ToolName: "search_files", Success: false, Error: "pattern must be a string"}
	}
	var matches []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if matched, _ := filepath.Match(pattern, info.Name()); matched {
			matches = append(matches, path)
		}
		if len(matches) >= 50 {
			return filepath.SkipAll
		}
		return nil
	})
	if len(matches) == 0 {
		return ToolResult{ToolName: "search_files", Success: true, Output: "no matches found"}
	}
	return ToolResult{ToolName: "search_files", Success: true, Output: strings.Join(matches, "\n")}
}

// commandRisk classifies a command's risk level.
// "safe"     — read-only, local (go build, python script.py, dir, type)
// "install"  — installs packages (pip install, npm install, go get)
// "network"  — makes external requests (curl, wget, Invoke-WebRequest)
// "destruct" — deletes or modifies system state (rm, del, format, reg)
func commandRisk(cmd string) string {
	lower := strings.ToLower(cmd)
	// Destructive
	for _, kw := range []string{"rm -rf", "del /", "rmdir /s", "format ", "reg delete", "reg add"} {
		if strings.Contains(lower, kw) {
			return "destruct"
		}
	}
	// Package installs (network + disk writes)
	for _, kw := range []string{"pip install", "pip3 install", "npm install", "npm i ", "go get ", "go install ", "cargo install", "choco install", "winget install", "scoop install"} {
		if strings.Contains(lower, kw) {
			return "install"
		}
	}
	// Network
	for _, kw := range []string{"curl ", "wget ", "invoke-webrequest", "invoke-restmethod", "iwr ", "irm "} {
		if strings.Contains(lower, kw) {
			return "network"
		}
	}
	return "safe"
}

// containsDangerousMetachars detects unquoted shell metacharacters that could enable command chaining.
// It returns true if dangerous metacharacters are found outside of quotes.
func containsDangerousMetachars(command string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false

	for i := 0; i < len(command); i++ {
		c := command[i]

		// Handle escape sequences
		if c == '\\' && i+1 < len(command) {
			i++ // Skip next character
			continue
		}

		// Track quote states
		if c == '\'' && !inDoubleQuote && !inBacktick {
			inSingleQuote = !inSingleQuote
			continue
		}
		if c == '"' && !inSingleQuote && !inBacktick {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if c == '`' && !inSingleQuote && !inDoubleQuote {
			inBacktick = !inBacktick
			continue
		}

		// When not in quotes, check for dangerous metacharacters
		if !inSingleQuote && !inDoubleQuote && !inBacktick {
			if (i+1 < len(command) && c == '&' && command[i+1] == '&') || // &&
				(i+1 < len(command) && c == '|' && command[i+1] == '|') || // ||
				c == '|' || c == ';' || c == '>' || c == '<' || c == '&' || c == '$' {
				return true
			}
			// Check for $(...) command substitution
			if c == '$' && i+1 < len(command) && command[i+1] == '(' {
				return true
			}
		}
	}

	return false
}

func toolRunCommand(args map[string]any) ToolResult {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return ToolResult{ToolName: "run_command", Success: false, Error: "command must be a string"}
	}
	workDir, ok := args["working_dir"].(string)
	if !ok {
		workDir = ""
	}

	// Safety check
	risk := commandRisk(command)
	safeMode := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_SAFE_MODE")))

	if risk != "safe" {
		log.Printf("run_command: [%s] %q", strings.ToUpper(risk), command)
	}

	if safeMode == "true" || safeMode == "1" {
		if risk == "destruct" {
			return ToolResult{ToolName: "run_command", Success: false,
				Error: fmt.Sprintf("BLOCKED by safe mode: destructive command %q — set JANUS_SAFE_MODE=false to allow", command)}
		}
		if risk == "install" {
			return ToolResult{ToolName: "run_command", Success: false,
				Error: fmt.Sprintf("BLOCKED by safe mode: package install %q — set JANUS_SAFE_MODE=false to allow", command)}
		}
		if risk == "network" {
			return ToolResult{ToolName: "run_command", Success: false,
				Error: fmt.Sprintf("BLOCKED by safe mode: network command %q — set JANUS_SAFE_MODE=false to allow", command)}
		}
	}

	// Resolve working_dir to absolute path so relative paths work correctly.
	if workDir != "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			workDir = abs
		}
	}

	// Prefer 'uv pip' over 'pip' when uv is available (faster, more reliable).
	if strings.Contains(strings.ToLower(command), "pip install") {
		if _, err := exec.LookPath("uv"); err == nil {
			command = strings.Replace(command, "pip3 install", "uv pip install", 1)
			command = strings.Replace(command, "pip install", "uv pip install", 1)
			log.Printf("run_command: upgraded to uv pip: %q", command)
		}
	}

	// Security: detect potentially dangerous command chains in the input.
	// This is a conservative check to prevent execution of chained commands
	// like "echo test && rm -rf /". We detect unquoted shell metacharacters.
	if containsDangerousMetachars(command) {
		return ToolResult{ToolName: "run_command", Success: false,
			Error: "command contains shell metacharacters (&&, ||, |, ;, >, <, &, $(), etc.). Commands must be single statements without chaining."}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	output := strings.TrimSpace(buf.String())
	if len(output) > 3000 {
		output = output[:3000] + "\n... [output truncated]"
	}

	if err != nil {
		msg := err.Error()
		if output != "" {
			msg = output
		}
		return ToolResult{ToolName: "run_command", Success: false, Error: msg, Output: output}
	}
	if output == "" {
		output = "(command completed with no output)"
	}
	return ToolResult{ToolName: "run_command", Success: true, Output: output}
}

func toolDelete(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolResult{ToolName: "delete", Success: false, Error: "path must be a string"}
	}
	if err := os.Remove(path); err != nil {
		return ToolResult{ToolName: "delete", Success: false, Error: err.Error()}
	}
	return ToolResult{ToolName: "delete", Success: true, Output: "deleted: " + path}
}

func toolDone(args map[string]any) ToolResult {
	summary, ok := args["summary"].(string)
	if !ok {
		summary = "task completed"
	}
	if summary == "" {
		summary = "task completed"
	}
	return ToolResult{ToolName: "done", Success: true, Output: summary}
}

func toolCreateDir(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolResult{ToolName: "create_dir", Success: false, Error: "path must be a string"}
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return ToolResult{ToolName: "create_dir", Success: false, Error: err.Error()}
	}
	return ToolResult{ToolName: "create_dir", Success: true, Output: "created: " + path}
}
