// Command agent is the Fury agent CLI. Exactly one provider is linked per build
// via a provider_* build tag (GO_AGENT_PLAN.md §3.1, §11).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ahhh/fury_agent/internal/agent"
	"github.com/ahhh/fury_agent/internal/buildinfo"
	"github.com/ahhh/fury_agent/internal/config"
	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/tools"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return agent.ExitBadConfig
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return cmdRun(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "build-info":
		return cmdBuildInfo(rest)
	case "validate-config":
		return cmdValidateConfig(rest)
	case "replay-log":
		return cmdReplayLog(rest)
	case "-h", "--help", "help":
		usage()
		return agent.ExitSuccess
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		return agent.ExitBadConfig
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `fury agent — single-provider model agent

Usage:
  agent run [flags] [instruction]   run one instruction (stdin/-input if omitted)
  agent doctor                      print effective config (secrets redacted)
  agent build-info                  print build metadata
  agent validate-config [flags]     validate configuration and exit
  agent replay-log <session-dir>    validate and summarize a session log

Run flags:
  --input <file>    read the instruction from a file
  --stream          use streaming model turns
  --config <file>   JSON config file
  --log-dir <dir>   log directory (default: logs)
  --strict-logging  stop the run on a logging failure
  --allow-root <d>  add an approved filesystem/process root (repeatable)
  --enable-shell    enable the shell tool
  --unrestricted    remove all tool policy gates (shell on, any path, any
    (--yolo)        command); every action is still fully logged. Trusted or
                    sandboxed environments only.
`)
}

// resolveConfig applies precedence: defaults < env < file < flags.
func resolveConfig(flags parsedFlags) (config.Invariants, config.Runtime, error) {
	inv := config.LoadInvariants()
	rt := config.ApplyEnv(config.Defaults())
	var err error
	if rt, err = config.LoadFile(rt, flags.configPath); err != nil {
		return inv, rt, fmt.Errorf("load config file: %w", err)
	}
	flags.apply(&rt)
	return inv, rt, config.Validate(inv, rt)
}

func cmdRun(args []string) int {
	flags, positional, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.ExitBadConfig
	}
	inv, rt, err := resolveConfig(flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		return agent.ExitBadConfig
	}
	instruction, err := readInstruction(positional, flags.inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "input error:", err)
		return agent.ExitBadConfig
	}
	if strings.TrimSpace(instruction) == "" {
		fmt.Fprintln(os.Stderr, "no instruction provided")
		return agent.ExitBadConfig
	}

	apiKey := ""
	if rt.APIKeyEnv != "" {
		apiKey = os.Getenv(rt.APIKeyEnv)
	}
	redactor := logging.NewRedactor(rt.RedactPatterns, secretLiterals(apiKey))

	logger, err := logging.New(logging.Options{
		Dir:       rt.LogDir,
		Strict:    rt.StrictLogging,
		Fsync:     rt.Fsync,
		HashChain: rt.HashChain,
		Redactor:  redactor,
	}, buildinfo.Get(), "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging init failed:", err)
		return agent.ExitLoggingFailed
	}
	defer logger.Close()

	_ = logger.Emit(logging.EventSessionStart, logging.EventIDs{}, map[string]any{
		"build":  buildinfo.Get(),
		"stream": rt.Stream,
	})

	provider, err := newCompiledProvider(inv, rt, redactor)
	if err != nil {
		fmt.Fprintln(os.Stderr, "provider init failed:", err)
		_ = logger.WriteSummary("error", nil, nil)
		return agent.ExitProviderAuth
	}

	registry := buildToolRegistry(rt)
	if rt.Unrestricted {
		fmt.Fprintln(os.Stderr, "WARNING: unrestricted tool policy — the agent may run any command and access any path. All actions are logged.")
		_ = logger.Emit(logging.EventPolicyDecision, logging.EventIDs{}, map[string]any{
			"policy":       "unrestricted",
			"shell":        true,
			"filesystem":   "unconfined",
			"command_gate": "none",
		})
	}
	effModel, _ := config.EffectiveModel(inv, rt)
	ag := agent.New(provider, registry, logger, agent.Config{
		Model:  effModel,
		Stream: rt.Stream,
		Limits: limitsFromRuntime(rt),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	result, runErr := ag.Run(ctx, instruction)

	status := "success"
	if runErr != nil {
		var le *agent.LimitError
		if errors.As(runErr, &le) {
			status = "limit_reached"
		} else {
			status = "error"
		}
	}
	_ = logger.WriteSummary(status, map[string]any{
		"model_calls": result.ModelCalls,
		"tool_calls":  result.ToolCalls,
	}, result.Usage)
	_ = logger.Emit(logging.EventSessionEnd, logging.EventIDs{}, map[string]any{"status": status})

	if deg := logger.Degraded(); deg != nil {
		fmt.Fprintln(os.Stderr, "warning: degraded logging:", deg)
	}
	if runErr != nil {
		var le *agent.LimitError
		if errors.As(runErr, &le) {
			fmt.Fprintln(os.Stderr, "stopped:", runErr)
		} else {
			fmt.Fprintln(os.Stderr, "run error:", runErr)
		}
	}
	if result.Text != "" {
		fmt.Println(result.Text)
	}
	fmt.Fprintf(os.Stderr, "\n[session %s in %s]\n", logger.SessionID(), logger.Dir())
	return agent.ExitCode(runErr)
}

func cmdDoctor(args []string) int {
	flags, _, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.ExitBadConfig
	}
	inv, rt, valErr := resolveConfig(flags)
	out := map[string]any{
		"invariants": inv,
		"runtime":    rt,
		"build":      buildinfo.Get(),
	}
	if valErr != nil {
		out["validation_error"] = valErr.Error()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	if valErr != nil {
		return agent.ExitBadConfig
	}
	return agent.ExitSuccess
}

func cmdBuildInfo(_ []string) int {
	b, _ := json.MarshalIndent(buildinfo.Get(), "", "  ")
	fmt.Println(string(b))
	return agent.ExitSuccess
}

func cmdValidateConfig(args []string) int {
	flags, _, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return agent.ExitBadConfig
	}
	if _, _, err := resolveConfig(flags); err != nil {
		fmt.Fprintln(os.Stderr, "invalid:", err)
		return agent.ExitBadConfig
	}
	fmt.Println("configuration valid")
	return agent.ExitSuccess
}

func readInstruction(positional []string, inputPath string) (string, error) {
	if inputPath != "" {
		b, err := os.ReadFile(inputPath)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if len(positional) > 0 {
		return strings.Join(positional, " "), nil
	}
	// Fall back to stdin if it is not a terminal.
	fi, _ := os.Stdin.Stat()
	if fi != nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", nil
}

func buildToolRegistry(rt config.Runtime) *tools.Registry {
	policy := tools.Policy{
		ApprovedRoots:  rt.ApprovedRoots,
		EnableShell:    rt.EnableShell,
		DefaultTimeout: 60 * time.Second,
		MaxOutputBytes: rt.MaxToolOutput,
		Unrestricted:   rt.Unrestricted,
	}
	// Unrestricted mode removes the gates: shell on, filesystem available with no
	// root confinement, any command in any working dir. Everything is still
	// logged in full.
	if policy.Unrestricted {
		policy.EnableShell = true
	}
	reg := tools.NewRegistry()
	reg.Register(tools.NewProcessTool(policy))
	if len(policy.ApprovedRoots) > 0 || policy.Unrestricted {
		reg.Register(tools.NewFileSystemTool(policy))
	}
	if policy.EnableShell {
		reg.Register(tools.NewShellTool(policy))
	}
	return reg
}

func limitsFromRuntime(rt config.Runtime) agent.Limits {
	l := agent.DefaultLimits()
	if rt.MaxModelCalls > 0 {
		l.MaxModelCalls = rt.MaxModelCalls
	}
	if rt.MaxToolCalls > 0 {
		l.MaxToolCalls = rt.MaxToolCalls
	}
	if rt.MaxDuration.D() > 0 {
		l.MaxDuration = rt.MaxDuration.D()
	}
	if rt.MaxToolOutput > 0 {
		l.MaxToolOutputByte = rt.MaxToolOutput
	}
	return l
}

// secretLiterals returns non-empty secret strings to mask in logs.
func secretLiterals(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
