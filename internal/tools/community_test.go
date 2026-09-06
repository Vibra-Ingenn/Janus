package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCommunityStoreOpenClose(t *testing.T) {
	dir := t.TempDir()
	cs, err := OpenCommunityStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cs.Close()
	if cs.Count() != 0 {
		t.Fatalf("expected 0 tools, got %d", cs.Count())
	}
	if _, err := os.Stat(filepath.Join(dir, "community_tools.db")); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}

func TestCommunityStoreSaveAndGet(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	def := CommunityToolDef{
		Name:             "upload_dropbox",
		Description:      "Upload file to Dropbox",
		Author:           "alice",
		Version:          "1.0",
		Tags:             []string{"cloud", "files"},
		Parameters:       map[string]string{"file_path": "Path to file", "folder": "Dropbox folder"},
		StatusBroadcast:  "dropbox-upload",
		EntitySelector:     "{{file_path}}",
		PrimaryOperation: "shell:python upload.py --file {{file_path}} --folder {{folder}}",
		StepLatency:  0,
		RemediationLogic:     "retry:2:backoff",
		AuditReceipt:     "",
		Enabled:          true,
	}
	if err := cs.Save(def); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := cs.Get("upload_dropbox")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Author != "alice" {
		t.Errorf("author: want alice got %s", got.Author)
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags: want 2 got %d", len(got.Tags))
	}
	if !got.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestCommunityStoreSaveUpsert(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	def := CommunityToolDef{
		Name: "my_tool", PrimaryOperation: "shell:echo hello", Enabled: true,
	}
	cs.Save(def)
	def.Description = "updated description"
	if err := cs.Save(def); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ := cs.Get("my_tool")
	if got.Description != "updated description" {
		t.Errorf("description not updated: %s", got.Description)
	}
	if cs.Count() != 1 {
		t.Errorf("expected 1 tool after upsert, got %d", cs.Count())
	}
}

func TestCommunityStoreDelete(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	cs.Save(CommunityToolDef{Name: "to_delete", PrimaryOperation: "shell:echo", Enabled: true})
	if err := cs.Delete("to_delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if cs.Count() != 0 {
		t.Error("expected 0 tools after delete")
	}
	if err := cs.Delete("to_delete"); err == nil {
		t.Error("expected error deleting non-existent tool")
	}
}

func TestCommunityStoreSetEnabled(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	cs.Save(CommunityToolDef{Name: "toggleable", PrimaryOperation: "shell:echo", Enabled: true})
	cs.SetEnabled("toggleable", false)
	enabled, _ := cs.ListEnabled()
	if len(enabled) != 0 {
		t.Errorf("expected 0 enabled tools, got %d", len(enabled))
	}
	all, _ := cs.List()
	if len(all) != 1 {
		t.Errorf("expected 1 total tool, got %d", len(all))
	}
	cs.SetEnabled("toggleable", true)
	enabled, _ = cs.ListEnabled()
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled tool after re-enable, got %d", len(enabled))
	}
}

func TestSubstituteTemplate(t *testing.T) {
	s := "shell:python upload.py --file {{file_path}} --folder {{folder}}"
	args := map[string]any{
		"file_path": "/tmp/report.pdf",
		"folder":    "/clinical",
	}
	got := SubstituteTemplate(s, args)
	want := "shell:python upload.py --file /tmp/report.pdf --folder /clinical"
	if got != want {
		t.Errorf("substitute:\nwant %q\n got %q", want, got)
	}
}

func TestSubstituteTemplatePartial(t *testing.T) {
	s := "shell:echo {{name}} {{missing}}"
	args := map[string]any{"name": "janus"}
	got := SubstituteTemplate(s, args)
	// {{missing}} should remain unchanged
	if got != "shell:echo janus {{missing}}" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestRegisterCommunityTools(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	cs.Save(CommunityToolDef{
		Name:             "greet_user",
		Description:      "Greet someone",
		Author:           "test",
		Parameters:       map[string]string{"name": "Person to greet"},
		PrimaryOperation: "shell:echo Hello {{name}}",
		RemediationLogic:     "fail",
		Enabled:          true,
	})

	reg := NewRegistry()
	var called string
	execFn := CommunityExecFunc(func(protocolID, command, target, signal, failLogic, audit string, bufMs int) ToolResult {
		called = command
		return ToolResult{ToolName: protocolID, Success: true, Output: "ok"}
	})
	if err := RegisterCommunityTools(cs, reg, execFn); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The AI calls the community tool
	result := reg.Dispatch(ToolCall{
		Name:      "greet_user",
		Arguments: map[string]any{"name": "Paul"},
	})
	if !result.Success {
		t.Errorf("dispatch failed: %s", result.Error)
	}
	if called != "shell:echo Hello Paul" {
		t.Errorf("substitution failed: got %q", called)
	}
}

func TestFetchPackContextCancel(t *testing.T) {
	dir := t.TempDir()
	cs, _ := OpenCommunityStore(dir)
	defer cs.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled
	_, err := cs.FetchPack(ctx, "http://127.0.0.1:19999/nonexistent")
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}
