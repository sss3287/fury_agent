//go:build provider_fake

package main

import (
	"github.com/ahhh/fury_agent/internal/config"
	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/provider/fake"
)

// compiledProviderGuard is declared by every compiled_provider_*.go. Selecting
// two provider tags redeclares it and fails the build (GO_AGENT_PLAN.md §3.1).
const compiledProviderGuard = "fake"

func newCompiledProvider(inv config.Invariants, _ config.Runtime, _ *logging.Redactor) (model.Provider, error) {
	return fake.New(model.ProviderIdentity{
		Provider:     inv.Provider,
		DefaultModel: inv.DefaultModel,
		BaseURL:      inv.BaseURL,
	}), nil
}
