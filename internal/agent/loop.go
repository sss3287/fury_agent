package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
)

// ToolRunner executes tool calls the model requests. Stage D provides a concrete
// implementation; the loop depends only on this interface.
type ToolRunner interface {
	// Definitions returns the tool declarations advertised to the model.
	Definitions() []model.ToolDefinition
	// Execute runs one tool call and returns a fully populated result. It must
	// not return an error for ordinary tool failures — those are encoded in the
	// ToolResult status so the model can react.
	Execute(ctx context.Context, call model.ToolCall) model.ToolResult
}

// Config holds per-run agent settings.
type Config struct {
	Model  string
	System []model.ContentPart
	Stream bool
	Limits Limits
}

// Agent drives a provider through model/tool turns with full logging.
type Agent struct {
	provider model.Provider
	tools    ToolRunner
	log      *logging.Logger
	cfg      Config
}

// New builds an Agent. tools may be nil to disable tool use.
func New(provider model.Provider, tools ToolRunner, log *logging.Logger, cfg Config) *Agent {
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	return &Agent{provider: provider, tools: tools, log: log, cfg: cfg}
}

// Result summarizes a completed run.
type Result struct {
	Text       string
	StopReason model.StopReason
	Usage      model.Usage
	TurnID     string
	ModelCalls int
	ToolCalls  int
}

// Run executes a single user instruction as one logical turn, which may span
// several model attempts because tools were executed (GO_AGENT_PLAN.md §5).
func (a *Agent) Run(ctx context.Context, instruction string) (Result, error) {
	ctr := newCounters()
	if a.cfg.Limits.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.cfg.Limits.MaxDuration)
		defer cancel()
	}

	conv := &conversation{system: a.cfg.System}
	conv.addUser(instruction)

	turnID := newTurnID()
	turn := logging.NewTurnRecord(a.log.SessionID(), turnID)
	_ = a.log.Emit(logging.EventTurnStart, logging.EventIDs{TurnID: turnID}, map[string]any{
		"instruction_bytes": len(instruction),
	})

	var (
		result   Result
		toolsRun int
	)
	result.TurnID = turnID

	for {
		if err := ctr.checkBeforeModelCall(a.cfg.Limits); err != nil {
			return a.finishTurn(turn, result, "limit_reached", err)
		}

		attemptID := newAttemptID()
		req := model.GenerateRequest{
			Model:     a.cfg.Model,
			System:    conv.system,
			Messages:  conv.messages,
			Stream:    a.cfg.Stream,
			MaxOutput: a.cfg.Limits.MaxTokens,
		}
		if a.tools != nil {
			req.Tools = a.tools.Definitions()
		}

		attempt, resp, err := a.callModel(ctx, turnID, attemptID, req, ctr)
		turn.Attempts = append(turn.Attempts, attempt)
		if err != nil {
			return a.finishTurn(turn, result, "error", err)
		}
		ctr.modelCalls++
		result.ModelCalls = ctr.modelCalls
		result.StopReason = resp.StopReason
		result.Usage = resp.Usage
		ctr.tokens += resp.Usage.TotalTokens

		conv.addAssistant(resp.Content)
		if t := resp.Text(); t != "" {
			result.Text = t
		}

		calls := resp.ToolCalls
		if len(calls) == 0 || a.tools == nil {
			return a.finishTurn(turn, result, "success", nil)
		}

		// Execute requested tools, then continue the same logical turn.
		results := make([]model.ToolResult, 0, len(calls))
		for _, call := range calls {
			if err := ctr.checkBeforeToolCall(a.cfg.Limits, toolsRun); err != nil {
				return a.finishTurn(turn, result, "limit_reached", err)
			}
			tr := a.execTool(ctx, turnID, call)
			turn.ToolExecutions = append(turn.ToolExecutions, rawJSON(tr))
			results = append(results, tr)
			ctr.toolCalls++
			toolsRun++
			result.ToolCalls = ctr.toolCalls
		}
		conv.addToolResults(results)
	}
}

// callModel performs one model attempt (streaming or not), logs it, and returns
// the turn-attempt record plus the accumulated response.
func (a *Agent) callModel(ctx context.Context, turnID, attemptID string, req model.GenerateRequest, ctr *counters) (logging.TurnAttempt, model.GenerateResponse, error) {
	start := time.Now()
	att := logging.TurnAttempt{AttemptID: attemptID, Request: rawJSON(req)}
	ctr.inputBytes += int64(len(att.Request))

	_ = a.log.Emit(logging.EventModelRequest, logging.EventIDs{TurnID: turnID, AttemptID: attemptID}, req)

	var (
		resp model.GenerateResponse
		err  error
	)
	fromSeq := a.log.NextSeq() + 1
	if req.Stream {
		resp, err = a.streamModel(ctx, turnID, attemptID, req, &att)
	} else {
		resp, err = a.provider.Generate(ctx, req)
	}
	att.DurationNS = time.Since(start).Nanoseconds()
	if err != nil {
		_ = a.log.Emit(logging.EventError, logging.EventIDs{TurnID: turnID, AttemptID: attemptID}, map[string]any{"error": err.Error()})
		return att, resp, err
	}
	if req.Stream {
		toSeq := a.log.NextSeq()
		att.StreamEventRange = &logging.SeqRange{From: fromSeq, To: toSeq}
	}
	att.Response = rawJSON(resp)
	att.RawResponse = json.RawMessage(resp.Raw)
	att.Usage = rawJSON(resp.Usage)
	ctr.outputBytes += int64(len(att.Response))

	_ = a.log.Emit(logging.EventModelResponse, logging.EventIDs{TurnID: turnID, AttemptID: attemptID}, resp)
	return att, resp, nil
}

// streamModel consumes a stream, logging each event and accumulating the final
// response.
func (a *Agent) streamModel(ctx context.Context, turnID, attemptID string, req model.GenerateRequest, att *logging.TurnAttempt) (model.GenerateResponse, error) {
	stream, err := a.provider.Stream(ctx, req)
	if err != nil {
		return model.GenerateResponse{}, err
	}
	defer stream.Close()

	acc := newAccumulator()
	for {
		ev, ok := stream.Next()
		if !ok {
			break
		}
		_ = a.log.Emit(logging.EventStreamEvent, logging.EventIDs{TurnID: turnID, AttemptID: attemptID}, ev)
		acc.add(ev)
	}
	if err := stream.Err(); err != nil {
		return acc.response(), err
	}
	return acc.response(), nil
}

// execTool validates and runs one tool call with full logging.
func (a *Agent) execTool(ctx context.Context, turnID string, call model.ToolCall) model.ToolResult {
	_ = a.log.Emit(logging.EventToolStart, logging.EventIDs{TurnID: turnID, ToolCallID: call.ID}, map[string]any{
		"name":      call.Name,
		"arguments": call.Arguments,
	})
	tr := a.tools.Execute(ctx, call)
	_ = a.log.Emit(logging.EventToolComplete, logging.EventIDs{TurnID: turnID, ToolCallID: call.ID}, tr)
	return tr
}

func (a *Agent) finishTurn(turn *logging.TurnRecord, result Result, status string, runErr error) (Result, error) {
	turn.Status = status
	turn.Output = logging.TurnOutput{Normalized: rawJSON(map[string]any{
		"text":        result.Text,
		"stop_reason": result.StopReason,
	})}
	if err := a.log.WriteTurn(turn); err != nil {
		// A logging failure in strict mode surfaces here.
		if runErr == nil {
			return result, fmt.Errorf("write turn: %w", err)
		}
	}
	_ = a.log.Emit(logging.EventTurnComplete, logging.EventIDs{TurnID: turn.TurnID}, map[string]any{
		"status":      status,
		"model_calls": result.ModelCalls,
		"tool_calls":  result.ToolCalls,
	})
	return result, runErr
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf("{%q:%q}", "marshal_error", err.Error()))
	}
	return b
}
