package agent

import (
	"context"
	"errors"

	"github.com/ahhh/fury_agent/internal/model"
)

// Exit codes (GO_AGENT_PLAN.md §11).
const (
	ExitSuccess       = 0
	ExitBadConfig     = 2
	ExitProviderAuth  = 3
	ExitProviderTemp  = 4
	ExitToolFailure   = 5
	ExitPolicyDenied  = 6
	ExitTimeout       = 7
	ExitLoggingFailed = 8
	ExitInternal      = 10
)

// ExitCode maps an error returned by Run to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitTimeout
	}
	var le *LimitError
	if errors.As(err, &le) {
		// Hitting a configured limit is a controlled stop, not a failure.
		return ExitSuccess
	}
	var pe *model.ProviderError
	if errors.As(err, &pe) {
		switch {
		case pe.HTTPStatus == 401 || pe.HTTPStatus == 403:
			return ExitProviderAuth
		case pe.HTTPStatus == 429 || pe.Retryable:
			return ExitProviderTemp
		default:
			return ExitProviderAuth
		}
	}
	var te *ToolError
	if errors.As(err, &te) {
		if te.Denied {
			return ExitPolicyDenied
		}
		return ExitToolFailure
	}
	return ExitInternal
}

// ToolError wraps a fatal tool failure that should stop the run.
type ToolError struct {
	Name   string
	Denied bool
	Err    error
}

func (e *ToolError) Error() string {
	if e.Err == nil {
		return "tool " + e.Name + " failed"
	}
	return "tool " + e.Name + ": " + e.Err.Error()
}

func (e *ToolError) Unwrap() error { return e.Err }
