package agent

import (
	"log"
	"strings"
)

func (a *Agent) generateSearchQuery(question string) (string, error) {
	messages := []Message{
		{
			Role:    "system",
			Content: QueryGenerationPrompt,
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

	lq := strings.ToLower(q)
	if q == "" ||
		strings.Contains(lq, "line") ||
		strings.Contains(lq, "number") ||
		strings.Contains(lq, "file") {
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
