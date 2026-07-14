// Package agent implements the provider-agnostic agent loop: it drives a
// compiled-in model provider through model turns and tool executions, writing a
// complete audit trail. The loop never knows which provider was linked
// (GO_AGENT_PLAN.md §5).
package agent

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/ahhh/fury_agent/internal/model"
)

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func newTurnID() string    { return newID("turn_") }
func newAttemptID() string { return newID("attempt_") }

// conversation holds the running message list for a session.
type conversation struct {
	system   []model.ContentPart
	messages []model.Message
}

func (c *conversation) addUser(text string) {
	c.messages = append(c.messages, model.Message{
		Role:  model.RoleUser,
		Parts: []model.ContentPart{model.Text(text)},
	})
}

func (c *conversation) addAssistant(parts []model.ContentPart) {
	c.messages = append(c.messages, model.Message{
		Role:  model.RoleAssistant,
		Parts: parts,
	})
}

// addToolResults appends tool results as a single tool-role message. Translation
// layers map this to the provider's expected shape (e.g. Anthropic user-role
// tool_result blocks, OpenAI tool messages).
func (c *conversation) addToolResults(results []model.ToolResult) {
	parts := make([]model.ContentPart, 0, len(results))
	for i := range results {
		parts = append(parts, model.ContentPart{
			Type:       model.PartToolResult,
			ToolResult: &results[i],
		})
	}
	c.messages = append(c.messages, model.Message{Role: model.RoleTool, Parts: parts})
}
