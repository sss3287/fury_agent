//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr places the child in its own process group so the whole group
// can be signalled on cancellation.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the child's process group.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Negative pid targets the process group led by the child.
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// signalOf extracts a terminating signal name from an exit error, if any.
func signalOf(exitErr *exec.ExitError) string {
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
