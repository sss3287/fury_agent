package gemini

import (
	"encoding/base64"
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
)

// toWireRequest translates a normalized request into a generateContent body,
// then merges req.Native over it (GO_AGENT_PLAN.md §3.4). The model is carried
// in the URL path, not the body.
func toWireRequest(req model.GenerateRequest) ([]byte, error) {
	w := wireRequest{}
	if sys := systemContent(req.System); sys != nil {
		w.SystemInstruction = sys
	}
	for _, m := range req.Messages {
		w.Contents = append(w.Contents, toWireContent(m))
	}
	if len(req.Tools) > 0 {
		decls := make([]wireFuncDecl, 0, len(req.Tools))
		for _, td := range req.Tools {
			decls = append(decls, wireFuncDecl{Name: td.Name, Description: td.Description, Parameters: td.Parameters})
		}
		w.Tools = []wireTool{{FunctionDeclarations: decls}}
	}
	if tc := toToolConfig(req.ToolChoice); tc != nil {
		w.ToolConfig = tc
	}

	gc := &wireGenConfig{Temperature: req.Temperature, MaxOutputTokens: req.MaxOutput}
	switch req.Output.Format {
	case model.OutputJSON:
		gc.ResponseMimeType = "application/json"
	case model.OutputJSONSchema:
		gc.ResponseMimeType = "application/json"
		gc.ResponseSchema = req.Output.Schema
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		gc.ThinkingConfig = &wireThinking{ThinkingBudget: req.Thinking.BudgetTokens, IncludeThoughts: true}
	}
	if genConfigSet(gc) {
		w.GenerationConfig = gc
	}

	body, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return mergeNative(body, req.Native)
}

func genConfigSet(gc *wireGenConfig) bool {
	return gc.Temperature != nil || gc.MaxOutputTokens != 0 || gc.ResponseMimeType != "" ||
		len(gc.ResponseSchema) != 0 || gc.ThinkingConfig != nil
}

func systemContent(parts []model.ContentPart) *wireContent {
	var wp []wirePart
	for _, p := range parts {
		if p.Type == model.PartText {
			wp = append(wp, wirePart{Text: p.Text})
		}
	}
	if len(wp) == 0 {
		return nil
	}
	return &wireContent{Parts: wp}
}

func toWireContent(m model.Message) wireContent {
	role := "user"
	if m.Role == model.RoleAssistant {
		role = "model"
	}
	// Tool results are sent as a user-role content with functionResponse parts.
	c := wireContent{Role: role}
	for _, p := range m.Parts {
		switch p.Type {
		case model.PartText:
			c.Parts = append(c.Parts, wirePart{Text: p.Text})
		case model.PartThinking:
			c.Parts = append(c.Parts, wirePart{Text: p.Thinking, Thought: true})
		case model.PartImage, model.PartDocument:
			if p.Blob != nil {
				c.Parts = append(c.Parts, wirePart{InlineData: &wireBlob{
					MimeType: p.Blob.MediaType,
					Data:     base64.StdEncoding.EncodeToString(p.Blob.Data),
				}})
			}
		case model.PartToolUse:
			if p.ToolUse != nil {
				c.Parts = append(c.Parts, wirePart{FunctionCall: &wireFunctionCall{
					Name: p.ToolUse.Name,
					Args: p.ToolUse.Arguments,
				}})
			}
		case model.PartToolResult:
			if p.ToolResult != nil {
				resp, _ := json.Marshal(map[string]string{"result": p.ToolResult.Content})
				c.Parts = append(c.Parts, wirePart{FunctionResponse: &wireFunctionResponse{
					Name:     p.ToolResult.Name,
					Response: resp,
				}})
			}
		}
	}
	return c
}

func toToolConfig(tc model.ToolChoice) json.RawMessage {
	mode := ""
	switch tc.Mode {
	case model.ToolChoiceAuto:
		mode = "AUTO"
	case model.ToolChoiceRequired, model.ToolChoiceTool:
		mode = "ANY"
	case model.ToolChoiceNone:
		mode = "NONE"
	default:
		return nil
	}
	cfg := map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
	if tc.Mode == model.ToolChoiceTool && tc.Name != "" {
		cfg["functionCallingConfig"].(map[string]any)["allowedFunctionNames"] = []string{tc.Name}
	}
	b, _ := json.Marshal(cfg)
	return b
}

// fromWireResponse translates a generateContent response into normalized form,
// preserving ordered parts and the raw body.
func fromWireResponse(raw []byte) (model.GenerateResponse, error) {
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return model.GenerateResponse{}, err
	}
	resp := model.GenerateResponse{
		Usage:      mapUsage(w.UsageMetadata),
		ResponseID: w.ResponseID,
		Model:      w.ModelVersion,
		Raw:        raw,
	}
	hasTool := false
	finish := ""
	if len(w.Candidates) > 0 {
		cand := w.Candidates[0]
		finish = cand.FinishReason
		for i, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				tc := model.ToolCall{
					ID:        toolCallID(p.FunctionCall.Name, i),
					Name:      p.FunctionCall.Name,
					Arguments: normalizeArgs(p.FunctionCall.Args),
				}
				resp.Content = append(resp.Content, model.ContentPart{Type: model.PartToolUse, ToolUse: &tc})
				resp.ToolCalls = append(resp.ToolCalls, tc)
				hasTool = true
			case p.Thought:
				resp.Content = append(resp.Content, model.ContentPart{Type: model.PartThinking, Thinking: p.Text})
			case p.Text != "":
				resp.Content = append(resp.Content, model.Text(p.Text))
			}
		}
	}
	resp.StopReason = mapFinishReason(finish, hasTool)
	return resp, nil
}

// toolCallID synthesizes a stable id since Gemini function calls carry no id.
func toolCallID(name string, index int) string {
	return "call_" + name + "_" + itoa(index)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func normalizeArgs(a json.RawMessage) json.RawMessage {
	if len(a) == 0 {
		return json.RawMessage(`{}`)
	}
	return a
}

func mapFinishReason(r string, hasTool bool) model.StopReason {
	if hasTool {
		return model.StopToolUse
	}
	switch r {
	case "STOP":
		return model.StopEndTurn
	case "MAX_TOKENS":
		return model.StopMaxTokens
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT":
		return model.StopContent
	case "":
		return ""
	default:
		return model.StopOther
	}
}

func mapUsage(u wireUsage) model.Usage {
	return model.Usage{
		InputTokens:       u.PromptTokenCount,
		OutputTokens:      u.CandidatesTokenCount,
		CachedInputTokens: u.CachedContentTokenCount,
		ReasoningTokens:   u.ThoughtsTokenCount,
		TotalTokens:       u.TotalTokenCount,
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
