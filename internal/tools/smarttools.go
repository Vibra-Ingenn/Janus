package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// registerSmartTools adds high-leverage tools that amplify each token.
// The AI outputs a small decision; Go does the heavy lifting.
func (r *Registry) registerSmartTools() {
	r.Register(ToolDef{
		Name:        "scaffold",
		Description: "Create an entire project skeleton in one call. Go generates all boilerplate — zero tokens wasted on file content. Returns the list of files created.",
		Parameters: map[string]string{
			"lang":    "Language: go, python, javascript, html, rust, c",
			"name":    "Project name (becomes the directory name)",
			"type":    "Project type: cli, web, lib, script (default: cli)",
			"modules": "Comma-separated list of module/file names to create (e.g. 'parser,handler,utils')",
		},
	}, toolScaffold)

	r.Register(ToolDef{
		Name:        "patch_file",
		Description: "Replace a specific string in a file. Only the old and new text are needed — far fewer tokens than rewriting the whole file.",
		Parameters: map[string]string{
			"path":       "File path to patch",
			"old_string": "Exact text to find (must be unique in file)",
			"new_string": "Replacement text",
		},
	}, toolPatchFile)

	r.Register(ToolDef{
		Name:        "append_file",
		Description: "Append content to the end of an existing file. Use for adding functions, routes, tests, etc. without rewriting the whole file.",
		Parameters: map[string]string{
			"path":    "File path to append to",
			"content": "Content to append",
		},
	}, toolAppendFile)

	r.Register(ToolDef{
		Name:        "multi_write",
		Description: "Write multiple small files in one call. Each entry is 'path:content' separated by '---FILE---'. Saves iterations vs writing one file at a time.",
		Parameters: map[string]string{
			"files": "Files in format: path1:content1---FILE---path2:content2---FILE---...",
		},
	}, toolMultiWrite)
}

// ── scaffold ─────────────────────────────────────────────────────────────────

var scaffoldTemplates = map[string]map[string]projectTemplate{
	"go": {
		"cli": {
			files: map[string]string{
				"main.go": goMainCLI,
				"go.mod":  goMod,
			},
			moduleFile: goModule,
			testFile:   goTest,
		},
		"lib": {
			files: map[string]string{
				"go.mod": goMod,
			},
			moduleFile: goModule,
			testFile:   goTest,
		},
	},
	"python": {
		"cli": {
			files: map[string]string{
				"main.py":          pyMainCLI,
				"requirements.txt": "",
			},
			moduleFile: pyModule,
			testFile:   pyTest,
		},
		"web": {
			files: map[string]string{
				"app.py":           pyWebApp,
				"requirements.txt": "flask>=3.0\n",
			},
			moduleFile: pyModule,
			testFile:   pyTest,
		},
		"script": {
			files: map[string]string{
				"main.py": pyScript,
			},
			moduleFile: pyModule,
		},
	},
	"javascript": {
		"cli": {
			files: map[string]string{
				"index.js":     jsMainCLI,
				"package.json": jsPackage,
			},
			moduleFile: jsModule,
		},
		"web": {
			files: map[string]string{
				"server.js":    jsWebServer,
				"package.json": jsPackageWeb,
			},
			moduleFile: jsModule,
		},
	},
	"html": {
		"web": {
			files: map[string]string{
				"index.html": htmlPage,
				"style.css":  htmlCSS,
				"script.js":  htmlJS,
			},
		},
	},
}

type projectTemplate struct {
	files      map[string]string // filename -> template content
	moduleFile string            // template for each module
	testFile   string            // template for test files
}

type tmplData struct {
	Name    string
	Module  string
	Modules []string
}

func toolScaffold(args map[string]any) ToolResult {
	lang, _ := args["lang"].(string)
	name, _ := args["name"].(string)
	ptype, _ := args["type"].(string)
	modsStr, _ := args["modules"].(string)

	if lang == "" || name == "" {
		return ToolResult{ToolName: "scaffold", Success: false, Error: "lang and name are required"}
	}
	if ptype == "" {
		ptype = "cli"
	}

	// Parse modules
	var modules []string
	for _, m := range strings.Split(modsStr, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			modules = append(modules, m)
		}
	}

	langTemplates, ok := scaffoldTemplates[lang]
	if !ok {
		// Fallback: create a generic project with empty files
		return scaffoldGeneric(name, lang, modules)
	}
	pt, ok := langTemplates[ptype]
	if !ok {
		// Try cli as default
		pt, ok = langTemplates["cli"]
		if !ok {
			return scaffoldGeneric(name, lang, modules)
		}
	}

	dir := filepath.Join("workspace", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{ToolName: "scaffold", Success: false, Error: err.Error()}
	}

	data := tmplData{Name: name, Modules: modules}
	var created []string

	// Write base files
	for filename, tmplStr := range pt.files {
		content := renderTemplate(tmplStr, data)
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return ToolResult{ToolName: "scaffold", Success: false, Error: err.Error()}
		}
		created = append(created, path)
	}

	// Write module files
	if pt.moduleFile != "" {
		for _, mod := range modules {
			data.Module = mod
			ext := langExt(lang)
			filename := mod + ext
			content := renderTemplate(pt.moduleFile, data)
			path := filepath.Join(dir, filename)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return ToolResult{ToolName: "scaffold", Success: false, Error: err.Error()}
			}
			created = append(created, path)
		}
	}

	// Write test files
	if pt.testFile != "" {
		for _, mod := range modules {
			data.Module = mod
			ext := langExt(lang)
			var testName string
			switch lang {
			case "go":
				testName = mod + "_test" + ext
			case "python":
				testName = "test_" + mod + ext
			default:
				testName = mod + ".test" + ext
			}
			content := renderTemplate(pt.testFile, data)
			path := filepath.Join(dir, testName)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return ToolResult{ToolName: "scaffold", Success: false, Error: err.Error()}
			}
			created = append(created, path)
		}
	}

	return ToolResult{
		ToolName: "scaffold",
		Success:  true,
		Output:   fmt.Sprintf("created %d files in %s/\n%s", len(created), dir, strings.Join(created, "\n")),
	}
}

func scaffoldGeneric(name, lang string, modules []string) ToolResult {
	dir := filepath.Join("workspace", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{ToolName: "scaffold", Success: false, Error: err.Error()}
	}
	ext := langExt(lang)
	var created []string

	// Create a main file
	mainFile := filepath.Join(dir, "main"+ext)
	_ = os.WriteFile(mainFile, []byte("// "+name+" — main entry point\n"), 0644)
	created = append(created, mainFile)

	for _, mod := range modules {
		path := filepath.Join(dir, mod+ext)
		_ = os.WriteFile(path, []byte("// "+mod+" module\n"), 0644)
		created = append(created, path)
	}

	return ToolResult{
		ToolName: "scaffold",
		Success:  true,
		Output:   fmt.Sprintf("created %d files in %s/\n%s", len(created), dir, strings.Join(created, "\n")),
	}
}

func langExt(lang string) string {
	switch lang {
	case "go":
		return ".go"
	case "python":
		return ".py"
	case "javascript":
		return ".js"
	case "rust":
		return ".rs"
	case "c":
		return ".c"
	case "html":
		return ".html"
	default:
		return ".txt"
	}
}

func renderTemplate(tmplStr string, data tmplData) string {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return tmplStr // fallback to raw string
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return tmplStr
	}
	return buf.String()
}

// ── patch_file ───────────────────────────────────────────────────────────────

func toolPatchFile(args map[string]any) ToolResult {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)

	if path == "" || oldStr == "" {
		return ToolResult{ToolName: "patch_file", Success: false, Error: "path and old_string are required"}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{ToolName: "patch_file", Success: false, Error: err.Error()}
	}

	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return ToolResult{ToolName: "patch_file", Success: false, Error: "old_string not found in file"}
	}
	if count > 1 {
		return ToolResult{ToolName: "patch_file", Success: false, Error: fmt.Sprintf("old_string found %d times — must be unique", count)}
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return ToolResult{ToolName: "patch_file", Success: false, Error: err.Error()}
	}

	return ToolResult{
		ToolName: "patch_file",
		Success:  true,
		Output:   fmt.Sprintf("patched %s — replaced %d chars with %d chars", path, len(oldStr), len(newStr)),
	}
}

// ── append_file ──────────────────────────────────────────────────────────────

func toolAppendFile(args map[string]any) ToolResult {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if path == "" {
		return ToolResult{ToolName: "append_file", Success: false, Error: "path is required"}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return ToolResult{ToolName: "append_file", Success: false, Error: err.Error()}
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return ToolResult{ToolName: "append_file", Success: false, Error: err.Error()}
	}

	return ToolResult{
		ToolName: "append_file",
		Success:  true,
		Output:   fmt.Sprintf("appended %d bytes to %s", len(content), path),
	}
}

// ── multi_write ──────────────────────────────────────────────────────────────

func toolMultiWrite(args map[string]any) ToolResult {
	raw, _ := args["files"].(string)
	if raw == "" {
		return ToolResult{ToolName: "multi_write", Success: false, Error: "files is required"}
	}

	parts := strings.Split(raw, "---FILE---")
	var created []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, ":")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(part[:idx])
		content := part[idx+1:]

		dir := filepath.Dir(path)
		if dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return ToolResult{ToolName: "multi_write", Success: false, Error: fmt.Sprintf("%s: %v", path, err)}
		}
		created = append(created, path)
	}

	return ToolResult{
		ToolName: "multi_write",
		Success:  true,
		Output:   fmt.Sprintf("wrote %d files:\n%s", len(created), strings.Join(created, "\n")),
	}
}

// ── Project Templates ────────────────────────────────────────────────────────
// These are Go text/templates. The AI never sees or generates this content.
// Each template is filled by Go natively — zero LLM tokens spent.

const goMainCLI = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: {{.Name}} <input>\n")
		os.Exit(1)
	}
	input := os.Args[1]
	fmt.Printf("{{.Name}}: processing %q\n", input)
	// TODO: wire modules here
}
`

const goMod = `module {{.Name}}

go 1.21
`

const goModule = `package main

// {{.Module}} handles the {{.Module}} logic for {{.Name}}.

// Process is the main entry point for the {{.Module}} module.
func {{.Module}}Process(input string) (string, error) {
	// TODO: implement {{.Module}} logic
	return input, nil
}
`

const goTest = `package main

import "testing"

func Test{{.Module}}Process(t *testing.T) {
	got, err := {{.Module}}Process("test input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty output")
	}
}
`

const pyMainCLI = `#!/usr/bin/env python3
"""{{.Name}} — CLI entry point."""

import sys
{{- range .Modules}}
from {{.}} import process as {{.}}_process
{{- end}}


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {{.Name}} <input>", file=sys.stderr)
        sys.exit(1)

    data = sys.argv[1]
    print(f"[{{.Name}}] processing: {data}")

    {{- range .Modules}}
    data = {{.}}_process(data)
    {{- end}}

    print(f"[{{.Name}}] result: {data}")


if __name__ == "__main__":
    main()
`

const pyModule = `"""{{.Module}} module for {{.Name}}."""


def process(data):
    """Process input data through the {{.Module}} stage.
    
    Args:
        data: Input data (any format).
    
    Returns:
        Processed data.
    """
    # TODO: implement {{.Module}} logic
    return data
`

const pyTest = `"""Tests for {{.Module}} module."""

from {{.Module}} import process


def test_process_returns_data():
    result = process("test input")
    assert result is not None


def test_process_handles_empty():
    result = process("")
    assert result == ""
`

const pyScript = `#!/usr/bin/env python3
"""{{.Name}} — standalone script."""

import sys
import json


def main():
    # Read from stdin or file argument
    if len(sys.argv) > 1:
        with open(sys.argv[1]) as f:
            data = f.read()
    else:
        data = sys.stdin.read()

    # TODO: process data
    result = data

    print(result)


if __name__ == "__main__":
    main()
`

const pyWebApp = `#!/usr/bin/env python3
"""{{.Name}} — web application."""

from flask import Flask, request, jsonify

app = Flask(__name__)


@app.route("/", methods=["GET"])
def index():
    return jsonify({"status": "ok", "app": "{{.Name}}"})


@app.route("/process", methods=["POST"])
def process():
    data = request.get_json(force=True)
    # TODO: process data
    return jsonify({"result": data})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=True)
`

const jsMainCLI = `#!/usr/bin/env node
"use strict";

{{- range .Modules}}
const {{.}} = require("./{{.}}");
{{- end}}

const input = process.argv[2];
if (!input) {
    console.error("Usage: node index.js <input>");
    process.exit(1);
}

console.log("[{{.Name}}] processing:", input);

let data = input;
{{- range .Modules}}
data = {{.}}.process(data);
{{- end}}

console.log("[{{.Name}}] result:", data);
`

const jsPackage = `{
  "name": "{{.Name}}",
  "version": "1.0.0",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  }
}
`

const jsWebServer = `"use strict";
const http = require("http");

const PORT = process.env.PORT || 3000;

const server = http.createServer((req, res) => {
    if (req.method === "POST" && req.url === "/process") {
        let body = "";
        req.on("data", chunk => body += chunk);
        req.on("end", () => {
            const data = JSON.parse(body);
            // TODO: process data
            res.writeHead(200, {"Content-Type": "application/json"});
            res.end(JSON.stringify({result: data}));
        });
    } else {
        res.writeHead(200, {"Content-Type": "application/json"});
        res.end(JSON.stringify({status: "ok", app: "{{.Name}}"}));
    }
});

server.listen(PORT, () => console.log("[{{.Name}}] listening on :" + PORT));
`

const jsPackageWeb = `{
  "name": "{{.Name}}",
  "version": "1.0.0",
  "main": "server.js",
  "scripts": {
    "start": "node server.js"
  }
}
`

const jsModule = `"use strict";

/**
 * {{.Module}} module for {{.Name}}.
 */
function process(data) {
    // TODO: implement {{.Module}} logic
    return data;
}

module.exports = { process };
`

const htmlPage = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Name}}</title>
    <link rel="stylesheet" href="style.css">
</head>
<body>
    <main>
        <h1>{{.Name}}</h1>
        <div id="app"></div>
    </main>
    <script src="script.js"></script>
</body>
</html>
`

const htmlCSS = `* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: system-ui, sans-serif; background: #0f172a; color: #e2e8f0; }
main { max-width: 800px; margin: 2rem auto; padding: 1rem; }
h1 { margin-bottom: 1rem; color: #38bdf8; }
`

const htmlJS = `"use strict";
document.addEventListener("DOMContentLoaded", () => {
    const app = document.getElementById("app");
    app.textContent = "{{.Name}} loaded.";
});
`
