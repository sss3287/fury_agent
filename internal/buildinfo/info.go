// Package buildinfo exposes compile-time build metadata. The provider and
// default model are embedded at build time via -ldflags -X so that a single
// binary is bound to exactly one provider family (see GO_AGENT_PLAN.md §3.1).
package buildinfo

import "runtime"

// These variables are overridden at link time. Example:
//
//	go build -ldflags "-X github.com/ahhh/fury_agent/internal/buildinfo.Provider=anthropic ..."
var (
	// Provider is the compiled provider family: openai, anthropic, gemini,
	// router, or fake. It is a compile-time constant for a given binary.
	Provider = "unset"
	// Model is the default model identifier embedded at build time.
	Model = "unset"
	// BaseURL optionally overrides the provider base URL.
	BaseURL = ""
	// Version is a git tag or semantic version.
	Version = "dev"
	// Commit is the git commit hash the binary was built from.
	Commit = "none"
	// Time is the UTC build timestamp.
	Time = "unknown"
	// ProviderTag is the Go build tag that selected the provider package.
	ProviderTag = "unset"
)

// Info is a snapshot of build metadata for logging and the build-info command.
type Info struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BaseURL     string `json:"base_url,omitempty"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Time        string `json:"build_time"`
	ProviderTag string `json:"provider_tag"`
	GoVersion   string `json:"go_version"`
	TargetOS    string `json:"target_os"`
	TargetArch  string `json:"target_arch"`
}

// Get returns the current build metadata snapshot.
func Get() Info {
	return Info{
		Provider:    Provider,
		Model:       Model,
		BaseURL:     BaseURL,
		Version:     Version,
		Commit:      Commit,
		Time:        Time,
		ProviderTag: ProviderTag,
		GoVersion:   runtime.Version(),
		TargetOS:    runtime.GOOS,
		TargetArch:  runtime.GOARCH,
	}
}
