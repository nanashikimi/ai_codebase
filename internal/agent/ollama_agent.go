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
	ToolName  string     `json:"tool_name,omitempty"`
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
	Type     string `json:"type"`
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
			Timeout: 180 * time.Second,
		},
		RepoRoot: ".",
	}
}

func (a *Agent) Chat(question string) (string, error) {
	system := `You are an AI Codebase Copilot.

Rules:
- Use tools to inspect repository code.
- Prefer search_code first, then open_file if needed.
- Do NOT invent file paths or line numbers.
- Never call open_file on a path that was not returned by search_code.
- If search_code returns no hits twice, answer exactly: No matches found in the repository.
- Final answers must include at least one real path:line citation from tool outputs.`

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
	zeroHitSearches := 0
	invalidFinalAnswers := 0
	knownPaths := map[string]bool{}

	for step := 0; step < 8; step++ {
		if len(messages) > 12 {
			messages = append([]Message{messages[0]}, messages[len(messages)-11:]...)
		}

		resp, err := a.callOllama(messages, toolsDef)
		if err != nil {
			return "", err
		}

		log.Printf("RAW MODEL CONTENT: %q", resp.Message.Content)

		if len(resp.Message.ToolCalls) > 0 {
			toolRounds++

			messages = append(messages, Message{
				Role:      "assistant",
				ToolCalls: resp.Message.ToolCalls,
			})

			for _, tc := range resp.Message.ToolCalls {
				if tc.Function.Name == "" {
					log.Printf("TOOL CALL ITEM: type=%q name=%q args=%v", tc.Type, tc.Function.Name, tc.Function.Arguments)
					continue
				}

				toolName := tc.Function.Name

				if toolName == "open_file" {
					path := getString(tc.Function.Arguments, "path")
					if path == "" || !knownPaths[path] {
						out := `{"error":"open_file rejected: path was not returned by search_code"}`
						messages = append(messages, Message{
							Role:     "tool",
							ToolName: toolName,
							Content:  out,
						})
						messages = append(messages, Message{
							Role:    "user",
							Content: "That open_file call was rejected. Use search_code first and only open a real path from search results.",
						})
						continue
					}
				}

				out, err := a.executeTool(toolName, tc.Function.Arguments)
				if err != nil {
					out = fmt.Sprintf(`{"error":%q}`, err.Error())
				}

				messages = append(messages, Message{
					Role:     "tool",
					ToolName: toolName,
					Content:  out,
				})

				appendToolSummary(&messages, toolName, out)
				addKnownPathsFromToolOutput(knownPaths, toolName, out)

				if toolName == "search_code" {
					if searchReturnedNoHits(out) {
						zeroHitSearches++
					} else {
						zeroHitSearches = 0
						haveToolContext = true
					}
				} else if toolName == "open_file" {
					haveToolContext = true
				}
			}
			if zeroHitSearches == 1 && len(knownPaths) == 0 {
				refs, err := a.forceContextAndContinue(&messages, question)
				if err != nil {
					return "", err
				}
				for _, p := range refs {
					knownPaths[p] = true
				}
				if len(refs) > 0 {
					haveToolContext = true
					messages = append(messages, Message{
						Role:    "user",
						Content: "Answer now using only the citations above. Include at least one real path:line citation.",
					})
					continue
				}
			}
			if zeroHitSearches >= 2 {
				return "No matches found in the repository.", nil
			}

			if toolRounds >= 2 {
				messages = append(messages, Message{
					Role:    "system",
					Content: "You now have enough context. Do not call any more tools. Answer using only the citations above.",
				})
			} else {
				messages = append(messages, Message{
					Role:    "user",
					Content: "If you need more context, call one more tool. Otherwise answer now using only the citations above.",
				})
			}

			continue
		}

		content := strings.TrimSpace(resp.Message.Content)
		log.Printf("TRIMMED CONTENT: %q", content)

		if content != "" && haveToolContext {
			if isNoMatchesAnswer(content) {
				if len(knownPaths) == 0 {
					return "No matches found in the repository.", nil
				}
			}

			if isCitationOnlyAnswer(content) && referencesKnownPath(content, knownPaths) {
				log.Printf("FINAL CITATION ANSWER RETURNED: %s", content)
				return content, nil
			}

			if !isScaffoldAnswer(content) && looksGrounded(content) && referencesKnownPath(content, knownPaths) {
				log.Printf("FINAL ANSWER RETURNED: %s", content)
				return content, nil
			}

			invalidFinalAnswers++
			if invalidFinalAnswers >= 2 {
				return conciseNoAnswer(), nil
			}

			messages = append(messages, Message{
				Role: "user",
				Content: "Your last answer was not grounded enough. " +
					"Answer again using ONLY the citations above. " +
					"Include at least one real path:line citation. " +
					"If you cannot answer from the tool outputs, answer exactly: No matches found in the repository.",
			})
			continue
		}

		refs, err := a.forceContextAndContinue(&messages, question)
		if err != nil {
			return "", err
		}
		for _, p := range refs {
			knownPaths[p] = true
		}

		if len(refs) == 0 {
			zeroHitSearches++
		} else {
			zeroHitSearches = 0
		}

		if zeroHitSearches >= 2 {
			return "No matches found in the repository.", nil
		}

		haveToolContext = true

		messages = append(messages, Message{
			Role:    "user",
			Content: "FINAL ANSWER NOW. Use ONLY the citations above and include at least one real path:line citation.",
		})
	}

	return "", errors.New("agent loop exceeded max steps")
}

// forceContextAndContinue generates a search query, searches the repo,
// picks the best hit, opens that file, and appends the context to messages.
// It returns the real file paths that were actually seen in tool outputs.
func (a *Agent) forceContextAndContinue(messages *[]Message, question string) ([]string, error) {
	searchQuery, err := a.generateSearchQuery(question)
	if err != nil || strings.TrimSpace(searchQuery) == "" {
		searchQuery = fallbackSearchQuery(question)
	}

	*messages = append(*messages, Message{
		Role:    "assistant",
		Content: "I'll inspect the repository using tools.",
	})

	*messages = append(*messages, Message{
		Role:    "user",
		Content: "Generated search query: " + searchQuery,
	})

	searchResp, err := tools.SearchCode(tools.SearchCodeRequest{
		Query:      searchQuery,
		Root:       a.RepoRoot,
		MaxResults: 8,
	})
	if err != nil {
		return nil, err
	}

	log.Printf("SEARCH HITS: %+v", searchResp.Hits)

	seenPaths := map[string]bool{}
	for _, h := range searchResp.Hits {
		if h.Path != "" {
			seenPaths[h.Path] = true
		}
	}

	searchJSON := mustJSON(searchResp)
	*messages = append(*messages, Message{
		Role:     "tool",
		ToolName: "search_code",
		Content:  searchJSON,
	})
	appendToolSummary(messages, "search_code", searchJSON)

	if len(searchResp.Hits) == 0 {
		fallbackQuery := fallbackSearchQuery(question)
		if fallbackQuery != "" && fallbackQuery != searchQuery {
			log.Printf("FALLBACK SEARCH QUERY: %s", fallbackQuery)

			fallbackResp, ferr := tools.SearchCode(tools.SearchCodeRequest{
				Query:      fallbackQuery,
				Root:       a.RepoRoot,
				MaxResults: 8,
			})
			if ferr == nil {
				searchResp = fallbackResp
				searchJSON = mustJSON(searchResp)

				*messages = append(*messages, Message{
					Role:     "tool",
					ToolName: "search_code",
					Content:  searchJSON,
				})
				appendToolSummary(messages, "search_code", searchJSON)

				for _, h := range searchResp.Hits {
					if h.Path != "" {
						seenPaths[h.Path] = true
					}
				}
			}
		}
	}

	if len(searchResp.Hits) > 0 {
		h := chooseBestHit(searchResp.Hits)
		if h != nil {
			log.Printf("OPENING FILE: %s at line %d", h.Path, h.Line)

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

				if openResp.Path != "" {
					seenPaths[openResp.Path] = true
				}
			} else {
				*messages = append(*messages, Message{
					Role:     "tool",
					ToolName: "open_file",
					Content:  fmt.Sprintf(`{"error":%q}`, oerr.Error()),
				})
			}
		}
	}

	*messages = append(*messages, Message{
		Role:    "user",
		Content: "Original question: " + question,
	})

	return mapKeys(seenPaths), nil
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

	start := time.Now()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
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
		fmt.Fprintf(os.Stderr, "OLLAMA RESP: tool_calls=%d content_len=%d\n",
			len(resp.Message.ToolCalls), len(resp.Message.Content))
	}

	log.Printf("OLLAMA LATENCY: %s", time.Since(start))
	return &resp, nil
}

func (a *Agent) executeTool(name string, args map[string]any) (string, error) {
	log.Printf("TOOL CALLED: %s args=%v", name, args)

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

func (a *Agent) generateSearchQuery(question string) (string, error) {
	messages := []Message{
		{
			Role: "system",
			Content: `You generate ripgrep search queries for code repositories.

Rules:
- Return ONLY code identifiers or short regex alternations.
- Do NOT include explanations.
- Do NOT include words like "line", "number", or "file".
- Prefer function names, endpoints, handlers, and server-related symbols.

Examples:
Question: Where is the HTTP server started?
Query: ListenAndServe|http\.ListenAndServe

Question: Where are HTTP handlers registered?
Query: HandleFunc|ServeHTTP

Question: Where is search_code implemented?
Query: SearchCode|search_code

Question: Where is /chat handled?
Query: /chat|HandleFunc|ServeHTTP

Return ONLY the query.`,
		},
		{
			Role:    "user",
			Content: question,
		},
	}

	resp, err := a.callOllama(messages, nil)
	if err != nil {
		return fallbackSearchQuery(question), nil
	}

	q := strings.TrimSpace(resp.Message.Content)
	q = strings.Trim(q, "`")
	q = strings.Trim(q, `"`)
	q = strings.ReplaceAll(q, "\n", " ")
	q = strings.TrimSpace(q)

	if strings.Contains(strings.ToLower(q), "line") ||
		strings.Contains(strings.ToLower(q), "number") ||
		strings.Contains(strings.ToLower(q), "file") ||
		q == "" {
		return fallbackSearchQuery(question), nil
	}

	log.Printf("generated search query: %s", q)
	return q, nil
}

func fallbackSearchQuery(question string) string {
	q := strings.ToLower(question)

	if strings.Contains(q, "/chat") {
		return `/chat|HandleFunc|ServeHTTP`
	}
	if strings.Contains(q, "handler") {
		return `HandleFunc|http\.HandleFunc|ServeHTTP`
	}
	if strings.Contains(q, "http server") || strings.Contains(q, "server") || strings.Contains(q, "started") {
		return `ListenAndServe|http\.ListenAndServe|http\.Server`
	}
	if strings.Contains(q, "search_code") {
		return `SearchCode|search_code`
	}
	if strings.Contains(q, "open_file") {
		return `OpenFile|open_file`
	}

	return `ListenAndServe|HandleFunc|ServeHTTP`
}

func appendToolSummary(messages *[]Message, toolName string, toolJSON string) {
	switch toolName {
	case "search_code":
		var r tools.SearchCodeResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			*messages = append(*messages, Message{
				Role:    "user",
				Content: "CITATIONS: (failed to parse search_code output)",
			})
			return
		}
		if len(r.Hits) == 0 {
			*messages = append(*messages, Message{
				Role:    "user",
				Content: "CITATIONS: (search_code returned 0 hits)",
			})
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

		*messages = append(*messages, Message{
			Role:    "user",
			Content: b.String(),
		})

	case "open_file":
		var r tools.OpenFileResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			*messages = append(*messages, Message{
				Role:    "user",
				Content: "OPEN_FILE SNIPPET: (failed to parse open_file output)",
			})
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
		fmt.Fprintf(&b, "OPEN_FILE SNIPPET (copy exact path:line): %s:%d-%d\n",
			r.Path, r.StartLine, r.StartLine+maxLines-1)

		for i := 0; i < maxLines; i++ {
			ln := r.StartLine + i
			fmt.Fprintf(&b, "%s:%d  %s\n", r.Path, ln, lines[i])
		}

		if r.Truncated {
			b.WriteString("(open_file output was truncated)\n")
		}

		*messages = append(*messages, Message{
			Role:    "user",
			Content: b.String(),
		})
	}
}

func looksGrounded(s string) bool {
	if !strings.Contains(s, ":") {
		return false
	}

	return strings.Contains(s, ".go:") ||
		strings.Contains(s, ".rs:") ||
		strings.Contains(s, ".py:") ||
		strings.Contains(s, ".ts:") ||
		strings.Contains(s, ".js:") ||
		strings.Contains(s, ".java:") ||
		strings.Contains(s, ".cpp:") ||
		strings.Contains(s, ".c:")
}

func referencesKnownPath(answer string, knownPaths map[string]bool) bool {
	for p := range knownPaths {
		if p != "" && strings.Contains(answer, p) {
			return true
		}
	}
	return false
}

func isNoMatchesAnswer(s string) bool {
	t := strings.TrimSpace(strings.ToLower(s))
	return t == "no matches found" ||
		t == "no matches found." ||
		t == "no matches found in the repository." ||
		t == "no matches found in the repository"
}

func searchReturnedNoHits(toolJSON string) bool {
	var r tools.SearchCodeResponse
	if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
		return false
	}
	return len(r.Hits) == 0
}

func addKnownPathsFromToolOutput(knownPaths map[string]bool, toolName string, toolJSON string) {
	switch toolName {
	case "search_code":
		var r tools.SearchCodeResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			return
		}
		for _, h := range r.Hits {
			if h.Path != "" {
				knownPaths[h.Path] = true
			}
		}

	case "open_file":
		var r tools.OpenFileResponse
		if err := json.Unmarshal([]byte(toolJSON), &r); err != nil {
			return
		}
		if r.Path != "" {
			knownPaths[r.Path] = true
		}
	}
}

func chooseBestHit(hits []tools.SearchHit) *tools.SearchHit {
	for i := range hits {
		if strings.Contains(hits[i].Text, "ListenAndServe") {
			return &hits[i]
		}
	}

	for i := range hits {
		if strings.Contains(hits[i].Text, `"/chat"`) {
			return &hits[i]
		}
	}

	for i := range hits {
		if strings.HasPrefix(hits[i].Path, "cmd/") {
			return &hits[i]
		}
	}

	for i := range hits {
		if strings.HasPrefix(hits[i].Path, "internal/agent/") {
			continue
		}
		return &hits[i]
	}

	if len(hits) > 0 {
		return &hits[0]
	}
	return nil
}

func conciseNoAnswer() string {
	return "I could not produce a grounded answer from the retrieved code snippets."
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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

func isScaffoldAnswer(s string) bool { //for 2nd attempt
	t := strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(t, "citations") ||
		strings.HasPrefix(t, "open_file snippet") ||
		strings.HasPrefix(t, "question:") ||
		strings.HasPrefix(t, "query:")
}

func isCitationOnlyAnswer(s string) bool {
	t := strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(t, "citations")
}
