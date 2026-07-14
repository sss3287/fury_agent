package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/ahhh/fury_agent/internal/model"
)

// ShellTool runs a command line through a shell. It is only usable when policy
// enables it (GO_AGENT_PLAN.md §8.2); otherwise Execute returns a denial.
type ShellTool struct {
	policy Policy
}

// NewShellTool builds a shell tool bound to policy.
func NewShellTool(p Policy) *ShellTool { return &ShellTool{policy: p} }

type shellArgs struct {
	Script         string `json:"script"`
	WorkingDir     string `json:"working_dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (t *ShellTool) Definition() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        "shell",
		Description: "Run a command line through the system shell. Disabled unless enabled by policy.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "script": {"type": "string", "description": "shell command line to execute"},
    "working_dir": {"type": "string"},
    "timeout_seconds": {"type": "integer"}
  },
  "required": ["script"]
}`),
	}
}

func (t *ShellTool) Execute(ctx context.Context, call model.ToolCall) model.ToolResult {
	if !t.policy.EnableShell {
		return errResult(call, model.ToolStatusDenied, "shell tool is disabled by policy")
	}
	var a shellArgs
	if err := json.Unmarshal(call.Arguments, &a); err != nil {
		return errResult(call, model.ToolStatusError, "invalid arguments: "+err.Error())
	}
	if a.Script == "" {
		return errResult(call, model.ToolStatusError, "script must not be empty")
	}
	shell, flag := shellCommand()
	workDir, err := (&ProcessTool{policy: t.policy}).resolveWorkDir(a.WorkingDir)
	if err != nil {
		return errResult(call, model.ToolStatusDenied, err.Error())
	}
	timeout := t.policy.DefaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	argv := []string{shell, flag, a.Script}
	rec := runCommand(ctx, commandSpec{
		exe:       shell,
		argv:      argv,
		workDir:   workDir,
		timeout:   timeout,
		useShell:  true,
		shellName: shell,
		maxOutput: t.policy.MaxOutputBytes,
	})
	return commandResult(call, rec)
}

func shellCommand() (shell, flag string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", "/C"
	}
	return "/bin/sh", "-c"
}
