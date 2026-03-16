package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nanashikimi/ai-codebase/internal/tools"
)

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

func isScaffoldAnswer(s string) bool {
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

func normalizeCitationAnswer(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= 1 {
		return s
	}

	if strings.HasPrefix(strings.ToLower(lines[0]), "citations") {
		return "Found relevant location:\n" + strings.Join(lines[1:], "\n")
	}

	return s
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
		if strings.HasSuffix(hits[i].Path, ".go") {
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
