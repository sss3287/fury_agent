// Package tools provides the agent's tool runtime: a registry that implements
// agent.ToolRunner, plus filesystem, process, and shell tools executed under an
// explicit policy with full audit records (GO_AGENT_PLAN.md §8).
package tools

import (
	"path/filepath"
	"strings"
	"time"
)

// Policy governs what tools may do. The zero value is restrictive: no approved
// roots, shell disabled.
type Policy struct {
	// ApprovedRoots limits filesystem and process working directories to these
	// absolute directory trees. Empty means the filesystem tool is unavailable.
	ApprovedRoots []string
	// EnableShell enables the shell tool. Disabled by default (§8.2).
	EnableShell bool
	// DefaultTimeout bounds a single tool execution when the call does not
	// specify one.
	DefaultTimeout time.Duration
	// MaxOutputBytes caps captured stdout/stderr per stream (§8.3).
	MaxOutputBytes int
	// AllowedCommands, when non-empty, restricts the process tool to these
	// executable base names.
	AllowedCommands []string
}

// DefaultPolicy returns conservative defaults with no roots and shell disabled.
func DefaultPolicy() Policy {
	return Policy{
		DefaultTimeout: 60 * time.Second,
		MaxOutputBytes: 256 << 10,
	}
}

// pathAllowed reports whether abs is within an approved root.
func (p Policy) pathAllowed(abs string) bool {
	abs = filepath.Clean(abs)
	for _, root := range p.ApprovedRoots {
		root = filepath.Clean(root)
		if abs == root {
			return true
		}
		if strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// commandAllowed reports whether an executable base name is permitted.
func (p Policy) commandAllowed(exe string) bool {
	if len(p.AllowedCommands) == 0 {
		return true
	}
	base := filepath.Base(exe)
	for _, a := range p.AllowedCommands {
		if a == base || a == exe {
			return true
		}
	}
	return false
}
