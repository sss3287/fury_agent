//go:build windows

package tools

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr creates a new process group so the child can be signalled
// independently of the agent.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup terminates the child process. Full job-object based tree
// termination is a future enhancement (GO_AGENT_PLAN.md §14.4).
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// signalOf is not meaningful on Windows; exit codes are used instead.
func signalOf(*exec.ExitError) string { return "" }
