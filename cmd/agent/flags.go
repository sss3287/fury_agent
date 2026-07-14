package main

import (
	"fmt"

	"github.com/ahhh/fury_agent/internal/config"
)

// parsedFlags holds command-line overrides that win over the config file.
type parsedFlags struct {
	inputPath    string
	configPath   string
	logDir       string
	stream       bool
	streamSet    bool
	strictLog    bool
	strictLogSet bool
	approvedRoot []string
	enableShell  bool
	enableSet    bool
	model        string
}

// apply overlays flag values onto rt (highest precedence except invariants).
func (f parsedFlags) apply(rt *config.Runtime) {
	if f.logDir != "" {
		rt.LogDir = f.logDir
	}
	if f.streamSet {
		rt.Stream = f.stream
	}
	if f.strictLogSet {
		rt.StrictLogging = f.strictLog
	}
	if len(f.approvedRoot) > 0 {
		rt.ApprovedRoots = append(rt.ApprovedRoots, f.approvedRoot...)
	}
	if f.enableSet {
		rt.EnableShell = f.enableShell
	}
	if f.model != "" {
		rt.ModelOverride = f.model
	}
}

// parseFlags parses run/doctor/validate flags. It uses a small hand-rolled
// parser so repeatable flags (--allow-root) and positional args mix cleanly.
func parseFlags(args []string) (parsedFlags, []string, error) {
	var f parsedFlags
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--input":
			f.inputPath, err = next()
		case "--config":
			f.configPath, err = next()
		case "--log-dir":
			f.logDir, err = next()
		case "--model":
			f.model, err = next()
		case "--allow-root":
			var v string
			if v, err = next(); err == nil {
				f.approvedRoot = append(f.approvedRoot, v)
			}
		case "--stream":
			f.stream, f.streamSet = true, true
		case "--no-stream":
			f.stream, f.streamSet = false, true
		case "--strict-logging":
			f.strictLog, f.strictLogSet = true, true
		case "--enable-shell":
			f.enableShell, f.enableSet = true, true
		default:
			if len(a) > 2 && a[:2] == "--" {
				return f, nil, fmt.Errorf("unknown flag: %s", a)
			}
			positional = append(positional, a)
		}
		if err != nil {
			return f, nil, err
		}
	}
	return f, positional, nil
}
