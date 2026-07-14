package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/ahhh/fury_agent/internal/model"
)

// toWireRequest translates a normalized request into a Responses request body,
// then merges req.Native over it (GO_AGENT_PLAN.md §3.4).
func toWireRequest(req model.GenerateRequest) ([]byte, error) {
	w := wireRequest{
		Model:           req.Model,
		Temperature:     req.Temperature,
		MaxOutputTokens: req.MaxOutput,
		Stream:          req.Stream,
		Metadata:        req.Metadata,
	}
	w.Instructions = systemText(req.System)
	for _, m := range req.Messages {
		w.Input = append(w.Input, toInputItems(m)...)
	}
	for _, td := range req.Tools {
		params := td.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		w.Tools = append(w.Tools, wireTool{Type: "function", Name: td.Name, Description: td.Description, Parameters: params})
	}
	if tc := toToolChoice(req.ToolChoice); tc != nil {
		w.ToolChoice = tc
	}
	if req.Output.Format == model.OutputJSON {
		w.Text = &wireTextConfig{Format: json.RawMessage(`{"type":"json_object"}`)}
	} else if req.Output.Format == model.OutputJSONSchema {
		name := req.Output.Name
		if name == "" {
			name = "output"
		}
		f, _ := json.Marshal(map[string]any{
			"type":   "json_schema",
			"name":   name,
			"schema": json.RawMessage(req.Output.Schema),
		})
		w.Text = &wireTextConfig{Format: f}
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		w.Reasoning = &wireReasoning{Effort: "medium"}
	}

	body, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return mergeNative(body, req.Native)
}

func systemText(parts []model.ContentPart) string {
	s := ""
	for _, p := range parts {
		if p.Type == model.PartText {
			if s != "" {
				s += "\n"
			}
			s += p.Text
		}
	}
	return s
}

// toInputItems converts one normalized message into one or more Responses input
// items. Tool results become function_call_output items; assistant tool calls
// become function_call items; text/images become a role message.
func toInputItems(m model.Message) []inputItem {
	if m.Role == model.RoleTool {
		var items []inputItem
		for _, p := range m.Parts {
			if p.Type == model.PartToolResult && p.ToolResult != nil {
				items = append(items, inputItem{
					Type:   "function_call_output",
					CallID: p.ToolResult.CallID,
					Output: p.ToolResult.Content,
				})
			}
		}
		return items
	}

	role := string(m.Role)
	textType := "input_text"
	if m.Role == model.RoleAssistant {
		textType = "output_text"
	}
	msg := inputItem{Type: "message", Role: role}
	var extra []inputItem
	for _, p := range m.Parts {
		switch p.Type {
		case model.PartText:
			msg.Content = append(msg.Content, inputPart{Type: textType, Text: p.Text})
		case model.PartImage:
			if p.Blob != nil {
				msg.Content = append(msg.Content, inputPart{Type: "input_image", ImageURL: blobURL(p.Blob)})
			}
		case model.PartDocument:
			if p.Blob != nil {
				msg.Content = append(msg.Content, inputPart{Type: "input_file", FileData: blobURL(p.Blob)})
			}
		case model.PartToolUse:
			if p.ToolUse != nil {
				extra = append(extra, inputItem{
					Type:      "function_call",
					CallID:    p.ToolUse.ID,
					Name:      p.ToolUse.Name,
					Arguments: string(p.ToolUse.Arguments),
				})
			}
		}
	}
	var out []inputItem
	if len(msg.Content) > 0 {
		out = append(out, msg)
	}
	out = append(out, extra...)
	return out
}

func blobURL(b *model.Blob) string {
	if b.URI != "" {
		return b.URI
	}
	return fmt.Sprintf("data:%s;base64,%s", b.MediaType, base64.StdEncoding.EncodeToString(b.Data))
}

func toToolChoice(tc model.ToolChoice) json.RawMessage {
	switch tc.Mode {
	case model.ToolChoiceAuto:
		return json.RawMessage(`"auto"`)
	case model.ToolChoiceNone:
		return json.RawMessage(`"none"`)
	case model.ToolChoiceRequired:
		return json.RawMessage(`"required"`)
	case model.ToolChoiceTool:
		b, _ := json.Marshal(map[string]string{"type": "function", "name": tc.Name})
		return b
	default:
		return nil
	}
}

// fromWireResponse translates a Responses result into normalized form,
// preserving ordered output items and the raw body.
func fromWireResponse(raw []byte) (model.GenerateResponse, error) {
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return model.GenerateResponse{}, err
	}
	resp := model.GenerateResponse{
		Usage:      mapUsage(w.Usage),
		ResponseID: w.ID,
		Model:      w.Model,
		Raw:        raw,
	}
	hasTool := false
	for _, item := range w.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					resp.Content = append(resp.Content, model.Text(part.Text))
				} else {
					nat, _ := json.Marshal(part)
					resp.Content = append(resp.Content, model.ContentPart{Type: model.PartNative, Native: nat})
				}
			}
		case "function_call":
			tc := model.ToolCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)}
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartToolUse, ToolUse: &tc})
			resp.ToolCalls = append(resp.ToolCalls, tc)
			hasTool = true
		case "reasoning":
			for _, s := range item.Summary {
				resp.Content = append(resp.Content, model.ContentPart{Type: model.PartThinking, Thinking: s.Text})
			}
		default:
			nat, _ := json.Marshal(item)
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartNative, Native: nat})
		}
	}
	resp.StopReason = mapStopReason(w, hasTool)
	return resp, nil
}

func mapStopReason(w wireResponse, hasTool bool) model.StopReason {
	if hasTool {
		return model.StopToolUse
	}
	if w.IncompleteDetails != nil {
		switch w.IncompleteDetails.Reason {
		case "max_output_tokens":
			return model.StopMaxTokens
		case "content_filter":
			return model.StopContent
		}
	}
	if w.Status == "completed" {
		return model.StopEndTurn
	}
	return model.StopOther
}

func mapUsage(u wireUsage) model.Usage {
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return model.Usage{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.InputTokensDetails.CachedTokens,
		ReasoningTokens:   u.OutputTokensDetails.ReasoningTokens,
		TotalTokens:       total,
	}
}

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
