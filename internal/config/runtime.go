package config

import (
	"encoding/json"
	"os"
	"time"
)

// Runtime holds operational settings resolved for a run (GO_AGENT_PLAN.md §10).
type Runtime struct {
	// Credentials.
	APIKeyEnv string `json:"api_key_env"`

	// Logging.
	LogDir            string `json:"log_dir"`
	LogLevel          string `json:"log_level"`
	RawContentLogging bool   `json:"raw_content_logging"`
	StrictLogging     bool   `json:"strict_logging"`
	Fsync             bool   `json:"fsync"`
	HashChain         bool   `json:"hash_chain"`

	// Transport.
	Timeout    Duration `json:"timeout"`
	MaxRetries int      `json:"max_retries"`

	// Loop limits.
	MaxModelCalls int      `json:"max_model_calls"`
	MaxToolCalls  int      `json:"max_tool_calls"`
	MaxDuration   Duration `json:"max_duration"`
	MaxToolOutput int      `json:"max_tool_output_bytes"`

	// Tool policy.
	ApprovedRoots []string `json:"approved_roots"`
	EnableShell   bool     `json:"enable_shell"`
	EnableEBPF    bool     `json:"enable_ebpf"`
	RequireEBPF   bool     `json:"require_ebpf"`
	NetworkAllow  []string `json:"network_allow"`
	// Unrestricted removes all tool policy gates (filesystem anywhere, any
	// command, shell enabled). Every action is still fully logged. Trusted or
	// sandboxed environments only.
	Unrestricted bool `json:"unrestricted"`

	// Behavior.
	Stream bool `json:"stream"`

	// ModelOverride is honored only in allow_model_override dev builds.
	ModelOverride string `json:"model_override,omitempty"`

	// RedactPatterns are extra regexes applied to logged content.
	RedactPatterns []string `json:"redact_patterns,omitempty"`
}

// Duration is a JSON-friendly time.Duration that accepts Go duration strings.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// Allow a bare number of nanoseconds too.
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 == nil {
			*d = Duration(n)
			return nil
		}
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Defaults returns the safe-default runtime configuration. Raw-content logging
// is on by default for this audit build (locked decision); transport secrets are
// always redacted regardless.
func Defaults() Runtime {
	return Runtime{
		APIKeyEnv:         "",
		LogDir:            "logs",
		LogLevel:          "info",
		RawContentLogging: true,
		StrictLogging:     false,
		Timeout:           Duration(120 * time.Second),
		MaxRetries:        3,
		MaxModelCalls:     20,
		MaxToolCalls:      50,
		MaxDuration:       Duration(10 * time.Minute),
		MaxToolOutput:     256 << 10,
		Stream:            false,
	}
}

// LoadFile merges a JSON config file over rt. A missing file is not an error.
func LoadFile(rt Runtime, path string) (Runtime, error) {
	if path == "" {
		return rt, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rt, nil
		}
		return rt, err
	}
	if err := json.Unmarshal(b, &rt); err != nil {
		return rt, err
	}
	return rt, nil
}

// ApplyEnv applies environment-variable overrides (lowest precedence above
// defaults). Only a few operational knobs are env-configurable.
func ApplyEnv(rt Runtime) Runtime {
	if v := os.Getenv("FURY_LOG_DIR"); v != "" {
		rt.LogDir = v
	}
	if v := os.Getenv("FURY_LOG_LEVEL"); v != "" {
		rt.LogLevel = v
	}
	if os.Getenv("FURY_STRICT_LOGGING") == "1" {
		rt.StrictLogging = true
	}
	if os.Getenv("FURY_FSYNC") == "1" {
		rt.Fsync = true
	}
	if os.Getenv("FURY_HASH_CHAIN") == "1" {
		rt.HashChain = true
	}
	if os.Getenv("FURY_UNRESTRICTED") == "1" {
		rt.Unrestricted = true
	}
	return rt
}
