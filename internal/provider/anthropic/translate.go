package anthropic

import (
	"encoding/base64"
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
)

const defaultMaxTokens = 4096

// toWireRequest translates a normalized request into the Messages request body,
// then merges req.Native over it so callers can set any field not modeled here
// (GO_AGENT_PLAN.md §3.4).
func toWireRequest(req model.GenerateRequest) ([]byte, error) {
	w := wireRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxOutput,
		Messages:    make([]wireMessage, 0, len(req.Messages)),
		Temperature: req.Temperature,
		Stream:      req.Stream,
		Metadata:    req.Metadata,
	}
	if w.MaxTokens <= 0 {
		w.MaxTokens = defaultMaxTokens
	}
	for _, s := range req.System {
		if s.Type == model.PartText {
			w.System = append(w.System, wireText{Type: "text", Text: s.Text})
		}
	}
	for _, m := range req.Messages {
		w.Messages = append(w.Messages, toWireMessage(m))
	}
	for _, td := range req.Tools {
		schema := td.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		w.Tools = append(w.Tools, wireTool{Name: td.Name, Description: td.Description, InputSchema: schema})
	}
	if tc := toToolChoice(req.ToolChoice); tc != nil {
		w.ToolChoice = tc
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		w.Thinking = &wireThinking{Type: "enabled", BudgetTokens: req.Thinking.BudgetTokens}
	}

	body, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return mergeNative(body, req.Native)
}

func toWireMessage(m model.Message) wireMessage {
	// Anthropic has only user/assistant roles; tool results ride in a user message.
	role := "user"
	if m.Role == model.RoleAssistant {
		role = "assistant"
	}
	wm := wireMessage{Role: role}
	for _, p := range m.Parts {
		wm.Content = append(wm.Content, toWireBlock(p)...)
	}
	return wm
}

func toWireBlock(p model.ContentPart) []wireBlock {
	switch p.Type {
	case model.PartText:
		return []wireBlock{{Type: "text", Text: p.Text}}
	case model.PartThinking:
		return []wireBlock{{Type: "thinking", Thinking: p.Thinking, Signature: p.Signature}}
	case model.PartToolUse:
		if p.ToolUse == nil {
			return nil
		}
		return []wireBlock{{Type: "tool_use", ID: p.ToolUse.ID, Name: p.ToolUse.Name, Input: p.ToolUse.Arguments}}
	case model.PartToolResult:
		if p.ToolResult == nil {
			return nil
		}
		content, _ := json.Marshal(p.ToolResult.Content)
		return []wireBlock{{
			Type:      "tool_result",
			ToolUseID: p.ToolResult.CallID,
			Content:   content,
			IsError:   p.ToolResult.Status == model.ToolStatusError,
		}}
	case model.PartImage, model.PartDocument:
		if p.Blob == nil {
			return nil
		}
		src := &wireSource{MediaType: p.Blob.MediaType}
		if p.Blob.URI != "" {
			src.Type = "url"
			src.URL = p.Blob.URI
		} else {
			src.Type = "base64"
			src.Data = base64.StdEncoding.EncodeToString(p.Blob.Data)
		}
		blockType := "image"
		if p.Type == model.PartDocument {
			blockType = "document"
		}
		return []wireBlock{{Type: blockType, Source: src}}
	default:
		return nil
	}
}

func toToolChoice(tc model.ToolChoice) json.RawMessage {
	switch tc.Mode {
	case model.ToolChoiceAuto:
		return json.RawMessage(`{"type":"auto"}`)
	case model.ToolChoiceRequired:
		return json.RawMessage(`{"type":"any"}`)
	case model.ToolChoiceNone:
		return json.RawMessage(`{"type":"none"}`)
	case model.ToolChoiceTool:
		b, _ := json.Marshal(map[string]string{"type": "tool", "name": tc.Name})
		return b
	default:
		return nil
	}
}

// fromWireResponse translates a Messages response into the normalized form,
// preserving ordered content blocks and the raw body.
func fromWireResponse(raw []byte) (model.GenerateResponse, error) {
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return model.GenerateResponse{}, err
	}
	resp := model.GenerateResponse{
		StopReason: mapStopReason(w.StopReason),
		Usage:      mapUsage(w.Usage),
		ResponseID: w.ID,
		Model:      w.Model,
		Raw:        raw,
	}
	for _, b := range w.Content {
		switch b.Type {
		case "text":
			resp.Content = append(resp.Content, model.Text(b.Text))
		case "thinking":
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartThinking, Thinking: b.Thinking, Signature: b.Signature})
		case "tool_use":
			tc := model.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input}
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartToolUse, ToolUse: &tc})
			resp.ToolCalls = append(resp.ToolCalls, tc)
		default:
			// Preserve unknown block types natively.
			nat, _ := json.Marshal(b)
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartNative, Native: nat})
		}
	}
	return resp, nil
}

func mapStopReason(s string) model.StopReason {
	switch s {
	case "end_turn":
		return model.StopEndTurn
	case "max_tokens":
		return model.StopMaxTokens
	case "tool_use":
		return model.StopToolUse
	case "stop_sequence":
		return model.StopStop
	case "":
		return ""
	default:
		return model.StopOther
	}
}

func mapUsage(u wireUsage) model.Usage {
	return model.Usage{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CacheReadInputTokens,
		TotalTokens:       u.InputTokens + u.OutputTokens,
	}
}

// mergeNative overlays sparse native JSON object fields onto body. Both must be
// JSON objects; native keys win.
func mergeNative(body, native json.RawMessage) ([]byte, error) {
	if len(native) == 0 {
		return body, nil
	}
	var base, over map[string]json.RawMessage
	if err := json.Unmarshal(body, &base); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(native, &over); err != nil {
		return nil, err
	}
	for k, v := range over {
		base[k] = v
	}
	return json.Marshal(base)
}
