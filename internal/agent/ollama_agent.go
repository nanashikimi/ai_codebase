package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nanashikimi/ai-codebase/internal/tools"
)

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []ToolDef      `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"` // for role=tool messages
}

type ToolCall struct {
	Type     string `json:"type"`
	Function struct {
		Index     int            `json:"index,omitempty"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type Agent struct {
	OllamaURL string
	Model     string
	Client    *http.Client
	RepoRoot  string
}

func NewDefaultAgent(model string) *Agent {
	return &Agent{
		OllamaURL: "http://127.0.0.1:11434/api/chat",
		Model:     model,
		Client: &http.Client{
			Timeout: 90 * time.Second,
		},
		RepoRoot: ".",
	}
}

func (a *Agent) Chat(question string) (string, error) {
	system := `You are an AI Codebase Copilot.

Rules:
- Use tools to inspect the repository for code questions.
- Prefer: search_code -> open_file.
- Do NOT invent line numbers. Use ONLY CITATIONS / OPEN_FILE SNIPPET blocks.
- Final answers must cite path:line.`

	messages := []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: question},
	}

	toolsDef := []ToolDef{
		makeSearchToolDef(),
		makeOpenFileToolDef(),
	}

	haveToolContext := false
	toolRounds := 0

	for step := 0; step < 24; step++ {
		resp, err := a.callOllama(messages, toolsDef)
		if err != nil {
			return "", err
		}

		// If model produced tool calls, execute them.
		if len(resp.Message.ToolCalls) > 0 {
			toolRounds++
			messages = append(messages, Message{
				Role:      "assistant",
				ToolCalls: resp.Message.ToolCalls,
			})

			for _, tc := range resp.Message.ToolCalls {
				if tc.Type != "function" {
					continue
				}
				toolName := tc.Function.Name

				out, err := a.executeTool(toolName, tc.Function.Arguments)
				if err != nil {
					out = fmt.Sprintf(`{"error":%q}`, err.Error())
				}

				// Raw tool output (machine-readable)
				messages = append(messages, Message{
					Role:     "tool",
					ToolName: toolName,
					Content:  out,
				})

				// Human-readable summary for the model (this is the key fix)
				appendToolSummary(&messages, toolName, out)

				haveToolContext = true
			}

			// After a couple of rounds, force finalization.
			if toolRounds >= 2 {
				messages = append(messages, Message{
					Role: "user",
					Content: "FINAL ANSWER NOW. Use ONLY the CITATIONS / OPEN_FILE SNIPPET blocks above for line numbers. Cite as path:line. Do NOT call any more tools.",
				})
			} else {
				messages = append(messages, Message{
					Role: "user",
					Content: "Continue only if you need more context. If you already have enough, give the FINAL answer with citations path:line using the CITATIONS/SNIPPET blocks.",
				})
			}
			continue
		}

		// If model returned a non-empty answer:
		if strings.TrimSpace(resp.Message.Content) != "" {
			// If we have tool context, accept the answer.
			if haveToolContext {
				return resp.Message.Content, nil
			}

			// Otherwise, don't trust it: force context once.
			if err := a.forceContextAndContinue(&messages, question); err != nil {
				return "", err
			}
			haveToolContext = true
			messages = append(messages, Message{
				Role: "user",
				Content: "FINAL ANSWER NOW. Use ONLY the CITATIONS / OPEN_FILE SNIPPET blocks above for line numbers. Cite as path:line.",
			})
			continue
		}

		// If model returned nothing: force context.
		if err := a.forceContextAndContinue(&messages, question); err != nil {
			return "", err
		}
		haveToolContext = true
		messages = append(messages, Message{
			Role: "user",
			Content: "FINAL ANSWER NOW. Use ONLY the CITATIONS / OPEN_FILE SNIPPET blocks above for line numbers. Cite as path:line.",
		})
	}

	return "", errors.New("agent loop exceeded max steps")
}

func (a *Agent) forceContextAndContinue(messages *[]Message, question string) error {
	// Search for likely server startup sites
	searchQuery := `http\.ListenAndServe|ListenAndServe`
	searchResp, err := tools.SearchCode(tools.SearchCodeRequest{
		Query:      searchQuery,
		Root:       a.RepoRoot,
		MaxResults: 8,
	})
	if err != nil {
		return err
	}

	searchJSON := mustJSON(searchResp)
	*messages = append(*messages,
		Message{Role: "assistant", Content: "I'll inspect the repository using tools."},
		Message{Role: "tool", ToolName: "search_code", Content: searchJSON},
	)
	appendToolSummary(messages, "search_code", searchJSON)
	/*
	// If we found something, open around the first hit
	if len(searchResp.Hits) > 0 {
		h := searchResp.Hits[0]
		start := h.Line - 20
		if start < 1 {
			start = 1
		}
		end := h.Line + 40

		openResp, oerr := tools.OpenFile(tools.OpenFileRequest{
			Path:      h.Path,
			StartLine: start,
			EndLine:   end,
		})
		if oerr == nil {
			openJSON := mustJSON(openResp)
			*messages = append(*messages, Message{Role: "tool", ToolName: "open_file", Content: openJSON})
			appendToolSummary(messages, "open_file", openJSON)
		} else {
			*messages = append(*messages, Message{
				Role:     "tool",
				ToolName: "open_file",
				Content:  fmt.Sprintf(`{"error":%q}`, oerr.Error()),
			})
		}
	}
	*/
	
	if len(searchResp.Hits) > 0 {
		var h *tools.SearchHit

		// 1) prefer cmd/
		for i := range searchResp.Hits {
			if strings.HasPrefix(searchResp.Hits[i].Path, "cmd/") {
				h = &searchResp.Hits[i]
				break
			}
		}

		// 2) otherwise skip internal/agent/
		if h == nil {
			for i := range searchResp.Hits {
				if strings.HasPrefix(searchResp.Hits[i].Path, "internal/agent/") {
					continue
				}
				h = &searchResp.Hits[i]
				break
			}
		}

		// 3) fallback to first hit
		if h == nil {
			h = &searchResp.Hits[0]
		}

		start := h.Line - 20
		if start < 1 {
			start = 1
		}
		end := h.Line + 40

		openResp, oerr := tools.OpenFile(tools.OpenFileRequest{
			Path:      h.Path,
			StartLine: start,
			EndLine:   end,
		})
		if oerr == nil {
			openJSON := mustJSON(openResp)
			*messages = append(*messages, Message{
				Role:     "tool",
				ToolName: "open_file",
				Content:  openJSON,
			})
			appendToolSummary(messages, "open_file", openJSON)
		} else {
			*messages = append(*messages, Message{
				Role:     "tool",
				ToolName: "open_file",
				Content:  fmt.Sprintf(`{"error":%q}`, oerr.Error()),
			})
		}
	}
	// Keep original question
	*messages = append(*messages, Message{
		Role:    "user",
		Content: "Original question: " + question,
	})

	return nil
}

func (a *Agent) callOllama(messages []Message, toolsDef []ToolDef) (*ChatResponse, error) {
	reqBody := ChatRequest{
		Model:    a.Model,
		Messages: messages,
		Tools:    toolsDef,
		Stream:   false,
		Options: map[string]any{
			"temperature": 0.2,
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", a.OllamaURL, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	r, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	if r.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama error: http %d", r.StatusCode)
	}

	var resp ChatResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return nil, err
	}

	log.Printf("ollama: tool_calls=%d content_len=%d done=%v role=%s",
		len(resp.Message.ToolCalls),
		len(strings.TrimSpace(resp.Message.Content)),
		resp.Done,
		resp.Message.Role,
	)

	if os.Getenv("AGENT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "OLLAMA RESP: tool_calls=%d content_len=%d\n", len(resp.Message.ToolCalls), len(resp.Message.Content))
	}

	return &resp, nil
}

func (a *Agent) executeTool(name string, args map[string]any) (string, error) {
	switch name {
	case "search_code":
		req := tools.SearchCodeRequest{
			Query:      getString(args, "query"),
			Root:       getString(args, "root"),
			MaxResults: getInt(args, "max_results"),
		}
		if req.Root == "" {
			req.Root = a.RepoRoot
		}
		resp, err := tools.SearchCode(req)
		if err != nil {
			return "", err
		}
		return mustJSON(resp), nil

	case "open_file":
		req := tools.OpenFileRequest{
			Path:      getString(args, "path"),
			StartLine: getInt(args, "start_line"),
			EndLine:   getInt(args, "end_line"),
			MaxChars:  getInt(args, "max_chars"),
		}
		resp, err := tools.OpenFile(req)
		if err != nil {
			return "", err
		}
		return mustJSON(resp), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func getString(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func getInt(m map[string]any, k string) int {
	v, ok := m[k]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}

func makeSearchToolDef() ToolDef {
	var t ToolDef
	t.Type = "function"
	t.Function.Name = "search_code"
	t.Function.Description = "Search the repository using ripgrep and return matching lines with file path and line number."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "Search query (ripgrep pattern)."},
			"root":        map[string]any{"type": "string", "description": "Optional root directory relative to server working dir."},
			"max_results": map[string]any{"type": "integer", "description": "Max number of hits to return."},
		},
		"required": []string{"query"},
	}
	return t
}

func makeOpenFileToolDef() ToolDef {
	var t ToolDef
	t.Type = "function"
	t.Function.Name = "open_file"
	t.Function.Description = "Open a file and return its contents for a given 1-based line range."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string", "description": "File path relative to repo root."},
			"start_line": map[string]any{"type": "integer", "description": "Start line (1-based)."},
			"end_line":   map[string]any{"type": "integer", "description": "End line (1-based, inclusive)."},
			"max_chars":  map[string]any{"type": "integer", "description": "Safety limit for returned text size."},
		},
		"required": []string{"path"},
	}
	return t
}

func appendToolSummary(messages *[]Message, toolName string, toolJSON string) {
	switch toolName {
	case "search_code":
		var r tools.SearchCodeResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			*messages = append(*messages, Message{Role: "user", Content: "CITATIONS: (failed to parse search_code output)"})
			return
		}
		if len(r.Hits) == 0 {
			*messages = append(*messages, Message{Role: "user", Content: "CITATIONS: (search_code returned 0 hits)"})
			return
		}

		max := 8
		if len(r.Hits) < max {
			max = len(r.Hits)
		}
		var b strings.Builder
		b.WriteString("CITATIONS (copy these exact path:line):\n")
		for i := 0; i < max; i++ {
			h := r.Hits[i]
			fmt.Fprintf(&b, "- %s:%d  %s\n", h.Path, h.Line, h.Text)
		}
		*messages = append(*messages, Message{Role: "user", Content: b.String()})

	case "open_file":
		var r tools.OpenFileResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			*messages = append(*messages, Message{Role: "user", Content: "OPEN_FILE SNIPPET: (failed to parse open_file output)"})
			return
		}

		lines := strings.Split(r.Content, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		maxLines := 25
		if len(lines) < maxLines {
			maxLines = len(lines)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "OPEN_FILE SNIPPET (copy exact path:line): %s:%d-%d\n", r.Path, r.StartLine, r.StartLine+maxLines-1)
		for i := 0; i < maxLines; i++ {
			ln := r.StartLine + i
			fmt.Fprintf(&b, "%s:%d  %s\n", r.Path, ln, lines[i])
		}
		if r.Truncated {
			b.WriteString("(open_file output was truncated)\n")
		}
		*messages = append(*messages, Message{Role: "user", Content: b.String()})
	}
}
