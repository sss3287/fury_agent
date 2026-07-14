package logging

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahhh/fury_agent/internal/buildinfo"
)

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret-value")
	h.Set("X-Api-Key", "sk-another")
	h.Set("Content-Type", "application/json")
	out := RedactHeaders(h)
	if out["Authorization"][0] != RedactedValue {
		t.Error("Authorization not redacted")
	}
	if out["X-Api-Key"][0] != RedactedValue {
		t.Error("X-Api-Key not redacted")
	}
	if out["Content-Type"][0] != "application/json" {
		t.Error("Content-Type should be preserved")
	}
	// Original header must be untouched.
	if h.Get("Authorization") != "Bearer sk-secret-value" {
		t.Error("original header mutated")
	}
}

func TestRedactorLiteralsAndPatterns(t *testing.T) {
	r := NewRedactor([]string{`sk-[a-zA-Z0-9]{6,}`}, []string{"my-secret-key-1234"})
	got := r.Apply("here is my-secret-key-1234 and token sk-ABCDEF123")
	if strings.Contains(got, "my-secret-key-1234") {
		t.Errorf("literal not redacted: %q", got)
	}
	if strings.Contains(got, "sk-ABCDEF123") {
		t.Errorf("pattern not redacted: %q", got)
	}
}

func TestCapTruncatesAndHashes(t *testing.T) {
	c := Cap([]byte("hello world"), 5)
	if string(c.Content) != "hello" || !c.Truncated || c.OriginalBytes != 11 {
		t.Errorf("bad cap: %+v", c)
	}
	if !strings.HasPrefix(c.SHA256, "sha256:") {
		t.Errorf("missing hash: %q", c.SHA256)
	}
	full := Cap([]byte("hi"), 0)
	if full.Truncated || string(full.Content) != "hi" {
		t.Errorf("no-cap should not truncate: %+v", full)
	}
}

func newTestLogger(t *testing.T, hashChain bool) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := New(Options{Dir: dir, HashChain: hashChain}, buildinfo.Get(), "ses_test")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	return l, l.Dir()
}

func TestLoggerWritesAndHashChain(t *testing.T) {
	l, sessionDir := newTestLogger(t, true)
	for i := 0; i < 5; i++ {
		if err := l.Emit(EventModelRequest, EventIDs{TurnID: "turn_1"}, map[string]any{"i": i}); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	scan, err := ScanEvents(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if scan.Count != 5 {
		t.Errorf("count = %d want 5", scan.Count)
	}
	if !scan.HashChainOK {
		t.Errorf("hash chain broken: %s", scan.HashError)
	}
}

func TestCrashRecoveryTruncatedTail(t *testing.T) {
	l, sessionDir := newTestLogger(t, true)
	for i := 0; i < 3; i++ {
		_ = l.Emit(EventModelRequest, EventIDs{}, map[string]any{"i": i})
	}
	_ = l.Close()

	// Simulate a crash mid-write: append a partial JSON line.
	path := filepath.Join(sessionDir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"event_id":"evt_partial","seq`)
	_ = f.Close()

	scan, err := ScanEvents(path)
	if err != nil {
		t.Fatalf("scan after crash: %v", err)
	}
	if scan.Count != 3 {
		t.Errorf("recovered %d complete events, want 3", scan.Count)
	}
	if !scan.TruncatedEnd {
		t.Error("expected TruncatedEnd = true")
	}
	if !scan.HashChainOK {
		t.Errorf("hash chain should be intact for complete events: %s", scan.HashError)
	}
}

func TestHashChainDetectsTampering(t *testing.T) {
	l, sessionDir := newTestLogger(t, true)
	for i := 0; i < 4; i++ {
		_ = l.Emit(EventModelRequest, EventIDs{}, map[string]any{"i": i})
	}
	_ = l.Close()

	path := filepath.Join(sessionDir, "events.jsonl")
	b, _ := os.ReadFile(path)
	// Corrupt a data value in the second line without fixing the hash.
	tampered := strings.Replace(string(b), `"i":1`, `"i":999`, 1)
	if tampered == string(b) {
		t.Fatal("test setup: nothing replaced")
	}
	_ = os.WriteFile(path, []byte(tampered), 0o600)

	scan, err := ScanEvents(path)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if scan.HashChainOK {
		t.Error("tampering should break the hash chain")
	}
}

func TestFilePermissionsRestrictive(t *testing.T) {
	l, sessionDir := newTestLogger(t, false)
	_ = l.Emit(EventSessionStart, EventIDs{}, nil)
	_ = l.Close()
	fi, err := os.Stat(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("events.jsonl perm = %v want 0600", fi.Mode().Perm())
	}
}
