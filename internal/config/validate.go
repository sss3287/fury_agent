package config

import (
	"fmt"
	"os"
)

// AllowModelOverride is set to true only by the allow_model_override build tag
// (see override_on.go / override_off.go). Production builds keep it false so a
// binary is bound to its compiled model (GO_AGENT_PLAN.md §3.1).
var AllowModelOverride = allowModelOverride

// EffectiveModel returns the model to use, honoring an override only when the
// build permits it.
func EffectiveModel(inv Invariants, rt Runtime) (string, error) {
	if rt.ModelOverride == "" || rt.ModelOverride == inv.DefaultModel {
		return inv.DefaultModel, nil
	}
	if !AllowModelOverride {
		return "", fmt.Errorf("model override %q rejected: this build is bound to model %q (rebuild with -tags allow_model_override to permit dev overrides)", rt.ModelOverride, inv.DefaultModel)
	}
	return rt.ModelOverride, nil
}

// Validate checks invariants and runtime settings before a run.
func Validate(inv Invariants, rt Runtime) error {
	if inv.Provider == "" || inv.Provider == "unset" {
		return fmt.Errorf("no provider compiled into this binary: build with a provider_* tag")
	}
	if inv.DefaultModel == "" || inv.DefaultModel == "unset" {
		return fmt.Errorf("no default model embedded: set -ldflags -X buildinfo.Model")
	}
	if _, err := EffectiveModel(inv, rt); err != nil {
		return err
	}
	if rt.LogDir == "" {
		return fmt.Errorf("log_dir must not be empty")
	}
	for _, root := range rt.ApprovedRoots {
		if fi, err := os.Stat(root); err != nil {
			return fmt.Errorf("approved root %q: %w", root, err)
		} else if !fi.IsDir() {
			return fmt.Errorf("approved root %q is not a directory", root)
		}
	}
	return nil
}
