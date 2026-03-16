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
	system := SystemPrompt
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
					Content: NoMoreToolsPrompt,
				})
			} else {
				messages = append(messages, Message{
					Role:    "user",
					Content: OneMoreToolOrAnswerPrompt,
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
				Role:    "user",
				Content: RetryGroundedAnswerPrompt,
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
			Content: FinalAnswerNowPrompt,
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
