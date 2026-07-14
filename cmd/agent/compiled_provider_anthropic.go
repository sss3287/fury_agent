//go:build provider_anthropic

package main

import (
	"fmt"
	"os"

	"github.com/ahhh/fury_agent/internal/config"
	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/provider/anthropic"
)

// compiledProviderGuard is declared by every compiled_provider_*.go. Selecting
// two provider tags redeclares it and fails the build (GO_AGENT_PLAN.md §3.1).
const compiledProviderGuard = "anthropic"

func newCompiledProvider(inv config.Invariants, rt config.Runtime, red *logging.Redactor) (model.Provider, error) {
	keyEnv := rt.APIKeyEnv
	if keyEnv == "" {
		keyEnv = "ANTHROPIC_API_KEY"
	}
	apiKey := os.Getenv(keyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("no API key: set %s", keyEnv)
	}
	effModel, err := config.EffectiveModel(inv, rt)
	if err != nil {
		return nil, err
	}
	return anthropic.New(anthropic.Config{
		BaseURL:    inv.BaseURL,
		APIKey:     apiKey,
		Model:      effModel,
		Timeout:    rt.Timeout.D(),
		MaxRetries: rt.MaxRetries,
		Redactor:   red,
	})
}
