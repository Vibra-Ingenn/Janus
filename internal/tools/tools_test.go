package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewRegistryHasBuiltins(t *testing.T) {
	r := NewRegistry()
	defs := r.Definitions()
	if len(defs) == 0 {
		t.Fatal("NewRegistry should have built-in tools")
	}

	// Check that key builtins exist
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"read_file", "write_file", "list_dir", "calculate", "get_time"} {
		if !names[want] {
			t.Errorf("missing built-in tool %q", want)
		}
	}
}

func TestRegisterCustomTool(t *testing.T) {
	r := NewRegistry()
	before := len(r.Definitions())

	r.Register(ToolDef{
		Name:        "my_tool",
		Description: "test tool",
		Parameters:  map[string]string{"x": "number"},
	}, func(args map[string]any) ToolResult {
		return ToolResult{ToolName: "my_tool", Success: true, Output: "ok"}
	})

	after := len(r.Definitions())
	if after != before+1 {
		t.Errorf("expected %d tools after register, got %d", before+1, after)
	}
}

func TestDispatchKnownTool(t *testing.T) {
	r := NewRegistry()
	result := r.Dispatch(ToolCall{Name: "get_time", Arguments: map[string]any{}})
	if !result.Success {
		t.Errorf("get_time dispatch failed: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("get_time should return non-empty output")
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	r := NewRegistry()
	result := r.Dispatch(ToolCall{Name: "nonexistent", Arguments: map[string]any{}})
	if result.Success {
		t.Error("unknown tool should return failure")
	}
	if !strings.Contains(result.Error, "unknown tool") {
		t.Errorf("error should mention unknown tool, got %q", result.Error)
	}
}

func TestParseToolCall(t *testing.T) {
	raw := `{"tool_call": {"name": "calculate", "arguments": {"expression": "2+2"}}}`
	tc, err := ParseToolCall(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Name != "calculate" {
		t.Errorf("name = %q, want calculate", tc.Name)
	}
	if tc.Arguments["expression"] != "2+2" {
		t.Errorf("expression = %v, want 2+2", tc.Arguments["expression"])
	}
}

func TestParseToolCallEmptyName(t *testing.T) {
	raw := `{"tool_call": {"name": "", "arguments": {}}}`
	_, err := ParseToolCall(raw)
	if err == nil {
		t.Error("should error on empty tool name")
	}
}

func TestParseToolCallInvalidJSON(t *testing.T) {
	_, err := ParseToolCall("not json")
	if err == nil {
		t.Error("should error on invalid JSON")
	}
}

func TestCalculateTool(t *testing.T) {
	r := NewRegistry()
	result := r.Dispatch(ToolCall{
		Name:      "calculate",
		Arguments: map[string]any{"expression": "3 + 4 * 2"},
	})
	if !result.Success {
		t.Errorf("calculate failed: %s", result.Error)
	}
}

func TestSystemPromptBlock(t *testing.T) {
	r := NewRegistry()
	block := r.SystemPromptBlock()
	if !strings.Contains(block, "tool_call") {
		t.Error("system prompt block should contain tool_call schema")
	}
	if !strings.Contains(block, "read_file") {
		t.Error("system prompt block should list read_file")
	}
}

// ── Universal tool tests ─────────────────────────────────────────────────

func TestRegistryHasUniversalTools(t *testing.T) {
	r := NewRegistry()
	want := []string{"auto_ingest", "ocr_extract", "docx_extract", "image_extract", "render_pdf", "render_docx"}
	names := make(map[string]bool)
	for _, d := range r.Definitions() {
		names[d.Name] = true
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("registry missing universal tool %q", n)
		}
	}
}

func TestAutoIngestOnText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(p, []byte("Patient presents with chest pain and shortness of breath."), 0644); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	res := r.Dispatch(ToolCall{Name: "auto_ingest", Arguments: map[string]any{"path": p}})
	if !res.Success {
		t.Fatalf("auto_ingest failed: %s", res.Error)
	}
	if !strings.Contains(res.Output, "detected=text") {
		t.Errorf("expected detected=text, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "chest pain") {
		t.Errorf("content missing: %s", res.Output)
	}
}

func TestAutoIngestMissingFile(t *testing.T) {
	r := NewRegistry()
	res := r.Dispatch(ToolCall{Name: "auto_ingest", Arguments: map[string]any{"path": "/nonexistent/file.txt"}})
	if res.Success {
		t.Error("should fail on missing file")
	}
}

func TestOCRExtractWithoutTesseract(t *testing.T) {
	// We can only verify the friendly error path when tesseract is absent.
	// If tesseract IS installed on the test machine, we just verify the tool
	// rejects a missing path. Either way, the tool must not panic.
	r := NewRegistry()
	res := r.Dispatch(ToolCall{Name: "ocr_extract", Arguments: map[string]any{"path": "/nonexistent/image.png"}})
	if res.Success {
		t.Error("should fail on missing file")
	}
}

func TestRenderPDFBasic(t *testing.T) {
	t.Cleanup(func() { os.RemoveAll("workspace/outputs") })
	r := NewRegistry()
	res := r.Dispatch(ToolCall{Name: "render_pdf", Arguments: map[string]any{
		"title":    "Test Report",
		"content":  "# Heading\n\nThis is a test paragraph.\n\n- bullet one\n- bullet two",
		"filename": "unit-test",
	}})
	if !res.Success {
		t.Fatalf("render_pdf failed: %s", res.Error)
	}
	if !strings.Contains(res.Output, "workspace/outputs/") {
		t.Errorf("expected output path, got: %s", res.Output)
	}
	// Verify the file exists and has the PDF magic bytes.
	entries, _ := os.ReadDir("workspace/outputs")
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-unit-test.pdf") {
			found = true
			data, _ := os.ReadFile(filepath.Join("workspace/outputs", e.Name()))
			if !strings.HasPrefix(string(data), "%PDF-") {
				t.Errorf("file is not a valid PDF")
			}
			break
		}
	}
	if !found {
		t.Error("PDF file not found in workspace/outputs")
	}
}

func TestPDFExtractRepairsMalformedStartXRef(t *testing.T) {
	t.Cleanup(func() { os.RemoveAll("workspace/outputs") })
	r := NewRegistry()
	render := r.Dispatch(ToolCall{Name: "render_pdf", Arguments: map[string]any{
		"title":    "Patient Intake",
		"content":  "Patient Name: Jane Smith\nChief Complaint: Headache",
		"filename": "broken-xref",
	}})
	if !render.Success {
		t.Fatalf("render_pdf failed: %s", render.Error)
	}
	path := strings.TrimSpace(strings.TrimPrefix(render.Output, "PDF created at "))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	re := regexp.MustCompile(`startxref\s+(\d+)`)
	matches := re.FindSubmatchIndex(data)
	if matches == nil {
		t.Fatal("startxref not found in rendered PDF")
	}
	declared := string(data[matches[2]:matches[3]])
	data = append([]byte(nil), data...)
	copy(data[matches[2]:matches[3]], []byte(fmt.Sprintf("%0*d", len(declared), 0)))

	brokenPath := filepath.Join(filepath.Dir(path), "broken-startxref.pdf")
	if err := os.WriteFile(brokenPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	res := r.Dispatch(ToolCall{Name: "pdf_extract", Arguments: map[string]any{"path": brokenPath}})
	if !res.Success {
		t.Fatalf("pdf_extract should repair malformed startxref: %s", res.Error)
	}
	if !strings.Contains(res.Output, "Jane Smith") {
		t.Fatalf("expected extracted text after repair, got: %s", res.Output)
	}
}

func TestPDFExtractSalvagesMalformedPDFText(t *testing.T) {
	r := NewRegistry()
	dir := t.TempDir()
	path := filepath.Join(dir, "salvage.pdf")
	data := []byte("%PDF-1.4\n1 0 obj\n<< /Length 44 >>\nstream\nBT\n/F1 12 Tf\n72 720 Td\n(Document Title: Test Content) Tj\nET\nendstream\nendobj\n%%EOF\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	res := r.Dispatch(ToolCall{Name: "pdf_extract", Arguments: map[string]any{"path": path}})
	if !res.Success {
		t.Fatalf("pdf_extract should salvage malformed PDF text: %s", res.Error)
	}
	if !strings.Contains(res.Output, "best-effort salvage") || !strings.Contains(res.Output, "Test Content") {
		t.Fatalf("expected salvaged text, got: %s", res.Output)
	}
}

func TestPDFDiagnoseReportsProblems(t *testing.T) {
	r := NewRegistry()
	dir := t.TempDir()
	path := filepath.Join(dir, "diagnose.pdf")
	data := []byte("%PDF-1.4\n1 0 obj\n<< /Length 44 >>\nstream\nBT\n/F1 12 Tf\n72 720 Td\n(Document Title: Test Content) Tj\nET\nendstream\nendobj\n%%EOF\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	res := r.Dispatch(ToolCall{Name: "pdf_diagnose", Arguments: map[string]any{"path": path}})
	if !res.Success {
		t.Fatalf("pdf_diagnose failed: %s", res.Error)
	}
	if !strings.Contains(res.Output, "missing trailer block") || !strings.Contains(res.Output, "recoverable text fragments") {
		t.Fatalf("expected diagnostic details, got: %s", res.Output)
	}
}

func TestRenderDOCXBasic(t *testing.T) {
	t.Cleanup(func() { os.RemoveAll("workspace/outputs") })
	r := NewRegistry()
	res := r.Dispatch(ToolCall{Name: "render_docx", Arguments: map[string]any{
		"title":    "Test Letter",
		"content":  "Dear Dr. Smith,\n\nThank you for the referral.\n\nSincerely,\nDr. Jones",
		"filename": "unit-docx",
	}})
	if !res.Success {
		t.Fatalf("render_docx failed: %s", res.Error)
	}
	// Verify the output is a valid ZIP (docx is a zip).
	entries, _ := os.ReadDir("workspace/outputs")
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-unit-docx.docx") {
			found = true
			data, _ := os.ReadFile(filepath.Join("workspace/outputs", e.Name()))
			if len(data) < 4 || data[0] != 0x50 || data[1] != 0x4B {
				t.Errorf("file is not a valid ZIP/DOCX")
			}
			break
		}
	}
	if !found {
		t.Error("DOCX file not found in workspace/outputs")
	}
}

func TestDOCXRoundTrip(t *testing.T) {
	// render_docx → docx_extract should recover the content.
	t.Cleanup(func() { os.RemoveAll("workspace/outputs") })
	r := NewRegistry()
	content := "Patient had a good response to treatment.\nFollow up in 2 weeks."
	res := r.Dispatch(ToolCall{Name: "render_docx", Arguments: map[string]any{
		"content":  content,
		"filename": "roundtrip",
	}})
	if !res.Success {
		t.Fatalf("render_docx failed: %s", res.Error)
	}
	// Pull the path out of the success message.
	path := strings.TrimSpace(strings.TrimPrefix(res.Output, "DOCX created at "))
	res2 := r.Dispatch(ToolCall{Name: "docx_extract", Arguments: map[string]any{"path": path}})
	if !res2.Success {
		t.Fatalf("docx_extract failed: %s", res2.Error)
	}
	if !strings.Contains(res2.Output, "good response to treatment") {
		t.Errorf("round-trip lost content: %s", res2.Output)
	}
}

