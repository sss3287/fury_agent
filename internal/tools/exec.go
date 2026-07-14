package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
)

// CommandRecord is the complete audit record for a process or shell execution
// (GO_AGENT_PLAN.md §8.3). It is stored in ToolResult.Native.
type CommandRecord struct {
	Executable string   `json:"executable"`
	Argv       []string `json:"argv"`
	Display    string   `json:"display"`
	UsedShell  bool     `json:"used_shell"`
	Shell      string   `json:"shell,omitempty"`
	WorkingDir string   `json:"working_dir"`
	EnvNames   []string `json:"env_names,omitempty"`
	StdinBytes int      `json:"stdin_bytes"`
	PID        int      `json:"pid,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationNS int64     `json:"duration_ns"`

	ExitCode   int    `json:"exit_code"`
	Signal     string `json:"signal,omitempty"`
	TimedOut   bool   `json:"timed_out"`
	Cancelled  bool   `json:"cancelled"`
	NetworkErr string `json:"error,omitempty"`

	Stdout logging.Capped `json:"stdout"`
	Stderr logging.Capped `json:"stderr"`
}

// commandSpec describes a command to run.
type commandSpec struct {
	exe       string
	argv      []string // full argv including argv[0]
	workDir   string
	stdin     []byte
	timeout   time.Duration
	useShell  bool
	shellName string
	maxOutput int
}

// runCommand executes spec, capturing full audit metadata. It never runs a shell
// unless spec.useShell is set. The returned record is always populated.
func runCommand(ctx context.Context, spec commandSpec) CommandRecord {
	rec := CommandRecord{
		Executable: spec.exe,
		Argv:       spec.argv,
		Display:    displayCommand(spec.argv),
		UsedShell:  spec.useShell,
		Shell:      spec.shellName,
		WorkingDir: spec.workDir,
		StdinBytes: len(spec.stdin),
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.timeout)
		defer cancel()
	}

	args := spec.argv[1:]
	cmd := exec.CommandContext(runCtx, spec.exe, args...)
	cmd.Dir = spec.workDir
	if len(spec.stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	setSysProcAttr(cmd)
	// Kill the whole process group on context cancellation so children die too.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	rec.StartedAt = time.Now().UTC()
	start := time.Now()
	err := cmd.Run()
	rec.DurationNS = time.Since(start).Nanoseconds()
	rec.EndedAt = time.Now().UTC()
	if cmd.Process != nil {
		rec.PID = cmd.Process.Pid
	}

	rec.Stdout = logging.Cap(stdout.Bytes(), spec.maxOutput)
	rec.Stderr = logging.Cap(stderr.Bytes(), spec.maxOutput)

	switch {
	case err == nil:
		rec.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			rec.ExitCode = exitErr.ExitCode()
			if sig := signalOf(exitErr); sig != "" {
				rec.Signal = sig
			}
		} else {
			rec.ExitCode = -1
			rec.NetworkErr = err.Error()
		}
		if de := runCtx.Err(); de != nil {
			switch {
			case errors.Is(de, context.DeadlineExceeded):
				rec.TimedOut = true
			case errors.Is(de, context.Canceled):
				rec.Cancelled = true
			}
		}
	}
	return rec
}

func displayCommand(argv []string) string {
	var b bytes.Buffer
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if bytes.ContainsAny([]byte(a), " \t\"'") {
			b.WriteByte('"')
			b.WriteString(a)
			b.WriteByte('"')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}
