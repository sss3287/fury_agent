// Package config resolves runtime configuration. Provider and default model are
// compile-time invariants (GO_AGENT_PLAN.md §10); runtime configuration is
// limited to operational settings. Precedence: compiled invariants > flags >
// config file > environment > safe defaults.
package config

import "github.com/ahhh/fury_agent/internal/buildinfo"

// Invariants are the compile-time values that runtime configuration may not
// override.
type Invariants struct {
	Provider     string
	DefaultModel string
	BaseURL      string
	ProviderTag  string
}

// LoadInvariants reads the compiled-in build metadata.
func LoadInvariants() Invariants {
	bi := buildinfo.Get()
	return Invariants{
		Provider:     bi.Provider,
		DefaultModel: bi.Model,
		BaseURL:      bi.BaseURL,
		ProviderTag:  bi.ProviderTag,
	}
}
