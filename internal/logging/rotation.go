package logging

// Capped is a size-limited byte payload plus truncation metadata. It is used
// wherever a field must be bounded before being written to the log
// (GO_AGENT_PLAN.md §9.5): request/response bodies, stdout, stderr, and
// stream-event retention.
type Capped struct {
	// Content holds up to Limit bytes of the original.
	Content []byte `json:"content"`
	// OriginalBytes is the full length before truncation.
	OriginalBytes int `json:"original_bytes"`
	// Truncated reports whether Content is shorter than the original.
	Truncated bool `json:"truncated"`
	// SHA256 is the digest of the complete original stream (§9.5), so integrity
	// is preserved even when stored content is truncated.
	SHA256 string `json:"sha256,omitempty"`
}

// Cap truncates b to at most limit bytes, hashing the full input. A limit <= 0
// means "no cap" (content is stored in full).
func Cap(b []byte, limit int) Capped {
	c := Capped{OriginalBytes: len(b), SHA256: SHA256Hex(b)}
	if limit <= 0 || len(b) <= limit {
		c.Content = b
		return c
	}
	c.Content = b[:limit]
	c.Truncated = true
	return c
}
