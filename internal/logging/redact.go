package logging

import (
	"net/http"
	"regexp"
	"strings"
)

// RedactedValue is the placeholder written in place of sensitive data.
const RedactedValue = "[REDACTED]"

// sensitiveHeaders are transport headers that must never be persisted
// (GO_AGENT_PLAN.md §9.4, §9.6). Comparison is case-insensitive.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"api-key":             true,
	"x-goog-api-key":      true,
	"x-goog-api-client":   false, // not secret; keep for diagnostics
	"openai-organization": false,
}

// RedactHeaders returns a copy of h with sensitive header values replaced by
// RedactedValue. The original map is never mutated.
func RedactHeaders(h http.Header) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = []string{RedactedValue}
			continue
		}
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// Redactor applies configurable redaction rules to strings (§9.6). It is safe
// for concurrent use after construction.
type Redactor struct {
	patterns []*regexp.Regexp
	// literals are exact secret strings (e.g. the API key) that must be masked
	// wherever they appear, including inside logged bodies.
	literals []string
}

// NewRedactor builds a Redactor from regex pattern strings and literal secrets.
// Invalid patterns are skipped.
func NewRedactor(patterns []string, literals []string) *Redactor {
	r := &Redactor{}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			r.patterns = append(r.patterns, re)
		}
	}
	for _, l := range literals {
		if len(l) >= 8 { // avoid masking trivially short strings
			r.literals = append(r.literals, l)
		}
	}
	return r
}

// Apply returns s with all configured literals and patterns replaced.
func (r *Redactor) Apply(s string) string {
	if r == nil {
		return s
	}
	for _, l := range r.literals {
		if l != "" && strings.Contains(s, l) {
			s = strings.ReplaceAll(s, l, RedactedValue)
		}
	}
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, RedactedValue)
	}
	return s
}

// ApplyBytes applies redaction to a byte slice, returning a new slice.
func (r *Redactor) ApplyBytes(b []byte) []byte {
	if r == nil || len(b) == 0 {
		return b
	}
	return []byte(r.Apply(string(b)))
}
