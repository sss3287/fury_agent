package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahhh/fury_agent/internal/model"
)

// FileSystemTool provides read/write/list/stat/search within approved roots
// (GO_AGENT_PLAN.md §8.2).
type FileSystemTool struct {
	policy Policy
}

// NewFileSystemTool builds a filesystem tool bound to policy.
func NewFileSystemTool(p Policy) *FileSystemTool { return &FileSystemTool{policy: p} }

type fsArgs struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Query     string `json:"query"`
	MaxBytes  int    `json:"max_bytes"`
}

func (t *FileSystemTool) Definition() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        "filesystem",
		Description: "Read, write, list, stat, or search files within approved directory roots.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "operation": {"type": "string", "enum": ["read", "write", "list", "stat", "search"]},
    "path": {"type": "string", "description": "target file or directory"},
    "content": {"type": "string", "description": "content for write"},
    "query": {"type": "string", "description": "substring to search for"},
    "max_bytes": {"type": "integer", "description": "cap on bytes read"}
  },
  "required": ["operation", "path"]
}`),
	}
}

func (t *FileSystemTool) Execute(_ context.Context, call model.ToolCall) model.ToolResult {
	var a fsArgs
	if err := json.Unmarshal(call.Arguments, &a); err != nil {
		return errResult(call, model.ToolStatusError, "invalid arguments: "+err.Error())
	}
	if len(t.policy.ApprovedRoots) == 0 {
		return errResult(call, model.ToolStatusDenied, "filesystem tool has no approved roots")
	}
	abs, err := filepath.Abs(a.Path)
	if err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	if !t.policy.pathAllowed(abs) {
		return errResult(call, model.ToolStatusDenied, fmt.Sprintf("path %q is outside approved roots", abs))
	}

	switch a.Operation {
	case "read":
		return t.read(call, abs, a.MaxBytes)
	case "write":
		return t.write(call, abs, a.Content)
	case "list":
		return t.list(call, abs)
	case "stat":
		return t.stat(call, abs)
	case "search":
		return t.search(call, abs, a.Query)
	default:
		return errResult(call, model.ToolStatusError, "unknown operation: "+a.Operation)
	}
}

func (t *FileSystemTool) read(call model.ToolCall, path string, maxBytes int) model.ToolResult {
	b, err := os.ReadFile(path)
	if err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	limit := t.policy.MaxOutputBytes
	if maxBytes > 0 && (limit == 0 || maxBytes < limit) {
		limit = maxBytes
	}
	truncated := false
	if limit > 0 && len(b) > limit {
		b = b[:limit]
		truncated = true
	}
	status := model.ToolStatusSuccess
	if truncated {
		status = model.ToolStatusTruncated
	}
	return okResult(call, status, string(b))
}

func (t *FileSystemTool) write(call model.ToolCall, path, content string) model.ToolResult {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	return okResult(call, model.ToolStatusSuccess, fmt.Sprintf("wrote %d bytes to %s", len(content), path))
}

func (t *FileSystemTool) list(call model.ToolCall, path string) model.ToolResult {
	entries, err := os.ReadDir(path)
	if err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	var b strings.Builder
	for _, e := range entries {
		kind := "f"
		if e.IsDir() {
			kind = "d"
		}
		fmt.Fprintf(&b, "%s\t%s\n", kind, e.Name())
	}
	return okResult(call, model.ToolStatusSuccess, b.String())
}

func (t *FileSystemTool) stat(call model.ToolCall, path string) model.ToolResult {
	fi, err := os.Stat(path)
	if err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	info := map[string]any{
		"name":     fi.Name(),
		"size":     fi.Size(),
		"mode":     fi.Mode().String(),
		"is_dir":   fi.IsDir(),
		"mod_time": fi.ModTime().UTC(),
	}
	structured, _ := json.Marshal(info)
	res := okResult(call, model.ToolStatusSuccess, string(structured))
	res.Structured = structured
	return res
}

func (t *FileSystemTool) search(call model.ToolCall, root, query string) model.ToolResult {
	if query == "" {
		return errResult(call, model.ToolStatusError, "query must not be empty")
	}
	var b strings.Builder
	matches := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(content), "\n") {
			if strings.Contains(line, query) {
				fmt.Fprintf(&b, "%s:%d:%s\n", p, i+1, strings.TrimSpace(line))
				matches++
				if matches >= 200 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return errResult(call, model.ToolStatusError, err.Error())
	}
	return okResult(call, model.ToolStatusSuccess, b.String())
}

func okResult(call model.ToolCall, status model.ToolStatus, content string) model.ToolResult {
	return model.ToolResult{
		CallID:    call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
		Status:    status,
		Content:   content,
	}
}
