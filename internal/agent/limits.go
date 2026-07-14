package agent

import (
	"fmt"
	"time"
)

// Limits bounds a run so it stops predictably (GO_AGENT_PLAN.md §5).
type Limits struct {
	MaxModelCalls     int           // total model calls per run
	MaxToolCalls      int           // total tool calls per run
	MaxToolCallsTurn  int           // tool calls per turn
	MaxDuration       time.Duration // wall-clock
	MaxInputBytes     int64         // cumulative request bytes
	MaxOutputBytes    int64         // cumulative response bytes
	MaxToolOutputByte int           // per tool execution output cap
	MaxRetries        int           // retry cap (advisory; transport enforces)
	// MaxTokens optionally caps cumulative tokens when usage is available.
	MaxTokens int
}

// DefaultLimits are conservative defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxModelCalls:     20,
		MaxToolCalls:      50,
		MaxToolCallsTurn:  25,
		MaxDuration:       10 * time.Minute,
		MaxInputBytes:     32 << 20,
		MaxOutputBytes:    32 << 20,
		MaxToolOutputByte: 256 << 10,
		MaxRetries:        3,
	}
}

// LimitReason names the limit that stopped a run.
type LimitReason string

const (
	LimitModelCalls  LimitReason = "max_model_calls"
	LimitToolCalls   LimitReason = "max_tool_calls"
	LimitDuration    LimitReason = "max_duration"
	LimitInputBytes  LimitReason = "max_input_bytes"
	LimitOutputBytes LimitReason = "max_output_bytes"
	LimitTokens      LimitReason = "max_tokens"
)

// LimitError reports that a configured limit was reached.
type LimitError struct {
	Reason LimitReason
}

func (e *LimitError) Error() string { return fmt.Sprintf("limit reached: %s", e.Reason) }

// counters tracks cumulative usage against Limits during a run.
type counters struct {
	modelCalls  int
	toolCalls   int
	inputBytes  int64
	outputBytes int64
	tokens      int
	started     time.Time
}

func newCounters() *counters { return &counters{started: time.Now()} }

// checkBeforeModelCall returns a LimitError if another model call is not allowed.
func (c *counters) checkBeforeModelCall(l Limits) error {
	if l.MaxModelCalls > 0 && c.modelCalls >= l.MaxModelCalls {
		return &LimitError{LimitModelCalls}
	}
	if l.MaxDuration > 0 && time.Since(c.started) >= l.MaxDuration {
		return &LimitError{LimitDuration}
	}
	if l.MaxInputBytes > 0 && c.inputBytes >= l.MaxInputBytes {
		return &LimitError{LimitInputBytes}
	}
	if l.MaxOutputBytes > 0 && c.outputBytes >= l.MaxOutputBytes {
		return &LimitError{LimitOutputBytes}
	}
	if l.MaxTokens > 0 && c.tokens >= l.MaxTokens {
		return &LimitError{LimitTokens}
	}
	return nil
}

// checkBeforeToolCall returns a LimitError if another tool call is not allowed.
func (c *counters) checkBeforeToolCall(l Limits, perTurn int) error {
	if l.MaxToolCalls > 0 && c.toolCalls >= l.MaxToolCalls {
		return &LimitError{LimitToolCalls}
	}
	if l.MaxToolCallsTurn > 0 && perTurn >= l.MaxToolCallsTurn {
		return &LimitError{LimitToolCalls}
	}
	return nil
}
