package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ahhh/fury_agent/internal/model"
)

func fsCall(t *testing.T, op, path, content string) model.ToolCall {
	t.Helper()
	args, _ := json.Marshal(fsArgs{Operation: op, Path: path, Content: content})
	return model.ToolCall{ID: "c1", Name: "filesystem", Arguments: args}
}

func TestUnrestrictedFilesystemReadsOutsideRoots(t *testing.T) {
	// Create a file in a directory that is NOT an approved root.
	dir := t.TempDir()
	file := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(file, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Restricted policy with no roots must deny.
	restricted := NewFileSystemTool(Policy{})
	if got := restricted.Execute(context.Background(), fsCall(t, "read", file, "")); got.Status != model.ToolStatusDenied {
		t.Errorf("restricted read: status = %q, want denied", got.Status)
	}

	// Unrestricted policy must allow the same read.
	unrestricted := NewFileSystemTool(Policy{Unrestricted: true, MaxOutputBytes: 1 << 20})
	got := unrestricted.Execute(context.Background(), fsCall(t, "read", file, ""))
	if got.Status != model.ToolStatusSuccess {
		t.Fatalf("unrestricted read: status = %q (%s)", got.Status, got.Content)
	}
	if got.Content != "top secret" {
		t.Errorf("content = %q", got.Content)
	}
}

func TestUnrestrictedProcessAnyWorkingDir(t *testing.T) {
	dir := t.TempDir() // not an approved root
	tool := NewProcessTool(Policy{Unrestricted: true, DefaultTimeout: 0, MaxOutputBytes: 1 << 20})
	wd, err := tool.resolveWorkDir(dir)
	if err != nil {
		t.Fatalf("unrestricted resolveWorkDir denied: %v", err)
	}
	if wd == "" {
		t.Error("expected the working dir to be accepted")
	}

	// Restricted with roots set must reject a dir outside them.
	restricted := NewProcessTool(Policy{ApprovedRoots: []string{t.TempDir()}})
	if _, err := restricted.resolveWorkDir(dir); err == nil {
		t.Error("restricted resolveWorkDir should reject a dir outside approved roots")
	}
}

func TestShellDisabledUnlessEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	args, _ := json.Marshal(shellArgs{Script: "echo hi"})
	call := model.ToolCall{ID: "c", Name: "shell", Arguments: args}

	off := NewShellTool(Policy{})
	if got := off.Execute(context.Background(), call); got.Status != model.ToolStatusDenied {
		t.Errorf("shell should be denied when disabled, got %q", got.Status)
	}

	on := NewShellTool(Policy{EnableShell: true, DefaultTimeout: 0, MaxOutputBytes: 1 << 20})
	got := on.Execute(context.Background(), call)
	if got.Status != model.ToolStatusSuccess {
		t.Fatalf("shell enabled: status = %q (%s)", got.Status, got.Content)
	}
}

func TestPathAllowedUnrestricted(t *testing.T) {
	p := Policy{Unrestricted: true}
	if !p.pathAllowed("/etc/passwd") || !p.pathAllowed("/any/where") {
		t.Error("unrestricted pathAllowed should permit any path")
	}
	if (Policy{}).pathAllowed("/etc/passwd") {
		t.Error("restricted pathAllowed should deny with no roots")
	}
}
