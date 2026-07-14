package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ahhh/fury_agent/internal/model"
)

// ProcessTool runs an argv array without a shell (GO_AGENT_PLAN.md §8.2).
type ProcessTool struct {
	policy Policy
}

// NewProcessTool builds a process tool bound to policy.
func NewProcessTool(p Policy) *ProcessTool { return &ProcessTool{policy: p} }

type processArgs struct {
	Command        []string `json:"command"`
	WorkingDir     string   `json:"working_dir"`
	Stdin          string   `json:"stdin"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

func (t *ProcessTool) Definition() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        "process",
		Description: "Execute a program directly (no shell) as an argv array and capture stdout, stderr, and exit status.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "array", "items": {"type": "string"}, "minItems": 1, "description": "argv array; command[0] is the executable"},
    "working_dir": {"type": "string", "description": "working directory (must be within an approved root)"},
    "stdin": {"type": "string", "description": "optional data written to the process stdin"},
    "timeout_seconds": {"type": "integer", "description": "optional per-call timeout"}
  },
  "required": ["command"]
}`),
	}
}

func (t *ProcessTool) Execute(ctx context.Context, call model.ToolCall) model.ToolResult {
	var a processArgs
	if err := json.Unmarshal(call.Arguments, &a); err != nil {
		return errResult(call, model.ToolStatusError, "invalid arguments: "+err.Error())
	}
	if len(a.Command) == 0 {
		return errResult(call, model.ToolStatusError, "command must not be empty")
	}
	exe := a.Command[0]
	if !t.policy.commandAllowed(exe) {
		return errResult(call, model.ToolStatusDenied, fmt.Sprintf("command %q is not permitted by policy", exe))
	}
	workDir, err := t.resolveWorkDir(a.WorkingDir)
	if err != nil {
		return errResult(call, model.ToolStatusDenied, err.Error())
	}
	// Resolve the executable for the record; PATH lookup failures surface as an
	// execution error rather than a panic.
	if resolved, lookErr := exec.LookPath(exe); lookErr == nil {
		exe = resolved
	}

	timeout := t.policy.DefaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}

	rec := runCommand(ctx, commandSpec{
		exe:       exe,
		argv:      a.Command,
		workDir:   workDir,
		stdin:     []byte(a.Stdin),
		timeout:   timeout,
		maxOutput: t.policy.MaxOutputBytes,
	})
	return commandResult(call, rec)
}

func (t *ProcessTool) resolveWorkDir(dir string) (string, error) {
	if dir == "" {
		if len(t.policy.ApprovedRoots) > 0 {
			return t.policy.ApprovedRoots[0], nil
		}
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if len(t.policy.ApprovedRoots) > 0 && !t.policy.pathAllowed(abs) {
		return "", fmt.Errorf("working_dir %q is outside approved roots", abs)
	}
	return abs, nil
}

// commandResult converts a CommandRecord into a ToolResult.
func commandResult(call model.ToolCall, rec CommandRecord) model.ToolResult {
	status := model.ToolStatusSuccess
	switch {
	case rec.TimedOut:
		status = model.ToolStatusTimeout
	case rec.Cancelled:
		status = model.ToolStatusCancelled
	case rec.NetworkErr != "":
		status = model.ToolStatusError
	case rec.ExitCode != 0:
		status = model.ToolStatusError
	}
	if rec.Stdout.Truncated || rec.Stderr.Truncated {
		if status == model.ToolStatusSuccess {
			status = model.ToolStatusTruncated
		}
	}
	native, _ := json.Marshal(rec)
	content := fmt.Sprintf("exit_code=%d\n--- stdout ---\n%s\n--- stderr ---\n%s",
		rec.ExitCode, string(rec.Stdout.Content), string(rec.Stderr.Content))
	res := model.ToolResult{
		CallID:     call.ID,
		Name:       call.Name,
		Arguments:  call.Arguments,
		Status:     status,
		Content:    content,
		StartedAt:  rec.StartedAt,
		EndedAt:    rec.EndedAt,
		DurationNS: rec.DurationNS,
		Native:     native,
	}
	if rec.NetworkErr != "" {
		res.Error = rec.NetworkErr
	}
	return res
}

func errResult(call model.ToolCall, status model.ToolStatus, msg string) model.ToolResult {
	return model.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
		Status:    status,
		Content:   msg,
		Error:     msg,
	}
}
