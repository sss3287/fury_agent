package logging

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahhh/fury_agent/internal/buildinfo"
)

// Options configures a session Logger.
type Options struct {
	// Dir is the root log directory. Session logs go under
	// Dir/<date>/<session_id>/.
	Dir string
	// Strict stops the run on a logging failure. When false, logging failures
	// are surfaced as a visible degraded-logging error but the run continues
	// (GO_AGENT_PLAN.md §9.5).
	Strict bool
	// Fsync forces an fsync after each write for audit-sensitive runs.
	Fsync bool
	// HashChain enables per-event hash chaining (§9.5).
	HashChain bool
	// Redactor, when set, is applied to persisted string content.
	Redactor *Redactor
}

// Logger writes a single session's audit logs. It is safe for concurrent use.
type Logger struct {
	opts      Options
	build     buildinfo.Info
	sessionID string
	dir       string

	mu       sync.Mutex
	events   *os.File
	eventsW  *bufio.Writer
	turns    *os.File
	turnsW   *bufio.Writer
	seq      atomic.Int64
	prevHash string
	start    time.Time
	startMon time.Time

	// degraded records the first logging failure in non-strict mode.
	degraded error
}

// New creates a session Logger and writes the session directory. The session ID
// is generated if empty.
func New(opts Options, build buildinfo.Info, sessionID string) (*Logger, error) {
	if sessionID == "" {
		sessionID = "ses_" + randID()
	}
	if opts.Dir == "" {
		opts.Dir = "logs"
	}
	date := time.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(opts.Dir, date, sessionID)
	if err := os.MkdirAll(filepath.Join(dir, "artifacts"), 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	ev, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open events log: %w", err)
	}
	tn, err := os.OpenFile(filepath.Join(dir, "turns.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = ev.Close()
		return nil, fmt.Errorf("open turns log: %w", err)
	}
	l := &Logger{
		opts:      opts,
		build:     build,
		sessionID: sessionID,
		dir:       dir,
		events:    ev,
		eventsW:   bufio.NewWriter(ev),
		turns:     tn,
		turnsW:    bufio.NewWriter(tn),
		start:     time.Now().UTC(),
		startMon:  time.Now(),
	}
	return l, nil
}

// SessionID returns the session identifier.
func (l *Logger) SessionID() string { return l.sessionID }

// Dir returns the session log directory.
func (l *Logger) Dir() string { return l.dir }

// Degraded returns the first logging error encountered in non-strict mode, or
// nil. Callers should surface it visibly at the end of a run.
func (l *Logger) Degraded() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.degraded
}

// NextSeq returns the next monotonic sequence number. Exposed so callers can
// reference event ranges in turn records.
func (l *Logger) NextSeq() int64 { return l.seq.Load() }

// Emit writes an event to events.jsonl. ids may be nil.
func (l *Logger) Emit(eventType EventType, ids EventIDs, data any) error {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return l.fail(fmt.Errorf("marshal event data: %w", err))
		}
		if l.opts.Redactor != nil {
			b = l.opts.Redactor.ApplyBytes(b)
		}
		raw = b
	}
	env := Envelope{
		SchemaVersion: SchemaVersion,
		EventID:       "evt_" + randID(),
		Sequence:      l.seq.Add(1),
		TimestampUTC:  time.Now().UTC(),
		MonotonicNS:   time.Since(l.startMon).Nanoseconds(),
		EventType:     eventType,
		SessionID:     l.sessionID,
		TurnID:        ids.TurnID,
		AttemptID:     ids.AttemptID,
		ToolCallID:    ids.ToolCallID,
		Provider:      l.build.Provider,
		Model:         l.build.Model,
		Build:         buildMeta(l.build),
		Data:          raw,
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.HashChain {
		env.PrevHash = l.prevHash
		h, err := hashEnvelope(env)
		if err != nil {
			return l.failLocked(fmt.Errorf("hash event: %w", err))
		}
		env.Hash = h
		l.prevHash = h
	}
	if err := l.writeLine(l.eventsW, l.events, env); err != nil {
		return l.failLocked(err)
	}
	return nil
}

// EventIDs bundles optional correlation IDs for an event.
type EventIDs struct {
	TurnID     string
	AttemptID  string
	ToolCallID string
}

// WriteTurn appends a completed turn record to turns.jsonl.
func (l *Logger) WriteTurn(rec *TurnRecord) error {
	rec.CompletedAt = time.Now().UTC()
	if !rec.StartedAt.IsZero() {
		rec.DurationNS = rec.CompletedAt.Sub(rec.StartedAt).Nanoseconds()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.writeLine(l.turnsW, l.turns, rec); err != nil {
		return l.failLocked(err)
	}
	return nil
}

// Summary is the run summary written to summary.json (§9.1).
type Summary struct {
	SchemaVersion string         `json:"schema_version"`
	SessionID     string         `json:"session_id"`
	StartedAt     time.Time      `json:"started_at"`
	CompletedAt   time.Time      `json:"completed_at"`
	DurationNS    int64          `json:"duration_ns"`
	Status        string         `json:"status"`
	Build         buildinfo.Info `json:"build"`
	Totals        map[string]any `json:"totals,omitempty"`
	Usage         any            `json:"usage,omitempty"`
	FinalHash     string         `json:"final_hash,omitempty"`
	Degraded      string         `json:"degraded,omitempty"`
}

// WriteSummary writes summary.json for the session.
func (l *Logger) WriteSummary(status string, totals map[string]any, usage any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := Summary{
		SchemaVersion: SchemaVersion,
		SessionID:     l.sessionID,
		StartedAt:     l.start,
		CompletedAt:   time.Now().UTC(),
		DurationNS:    time.Since(l.startMon).Nanoseconds(),
		Status:        status,
		Build:         l.build,
		Totals:        totals,
		Usage:         usage,
		FinalHash:     l.prevHash,
	}
	if l.degraded != nil {
		s.Degraded = l.degraded.Error()
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return l.failLocked(err)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "summary.json"), b, 0o600); err != nil {
		return l.failLocked(err)
	}
	return nil
}

// Close flushes and closes the log files.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var first error
	for _, w := range []*bufio.Writer{l.eventsW, l.turnsW} {
		if err := w.Flush(); err != nil && first == nil {
			first = err
		}
	}
	for _, f := range []*os.File{l.events, l.turns} {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// writeLine marshals v, appends a newline, flushes, and optionally fsyncs.
// Caller must hold l.mu.
func (l *Logger) writeLine(w *bufio.Writer, f *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal log record: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	if err := w.WriteByte('\n'); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if l.opts.Fsync {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	return nil
}

// fail records or returns a logging error per strict/degraded policy.
func (l *Logger) fail(err error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failLocked(err)
}

// failLocked applies logging-failure policy. Caller must hold l.mu.
func (l *Logger) failLocked(err error) error {
	if err == nil {
		return nil
	}
	if l.opts.Strict {
		return err
	}
	if l.degraded == nil {
		l.degraded = err
	}
	return nil // continue in degraded mode
}

func randID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
