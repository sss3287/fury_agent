package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// hashEnvelope computes the canonical SHA-256 of an envelope with its own Hash
// field cleared, so each event can chain to the previous one (GO_AGENT_PLAN.md
// §9.5). The result is a lowercase hex string prefixed with "sha256:".
func hashEnvelope(e Envelope) (string, error) {
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// SHA256Hex returns the "sha256:"-prefixed hex digest of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
