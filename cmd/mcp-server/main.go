package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/nanashikimi/ai-codebase/internal/agent"
	"github.com/nanashikimi/ai-codebase/internal/tools"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func setCORS(w http.ResponseWriter) { //for different origins
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func main() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	http.HandleFunc("/tools/search_code", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		var req tools.SearchCodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid json"})
			return
		}
		resp, err := tools.SearchCode(req)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, resp)
	})

	http.HandleFunc("/tools/open_file", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		var req tools.OpenFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid json"})
			return
		}
		resp, err := tools.OpenFile(req)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, resp)
	})

	// Existing simple API
	http.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}

		var body struct {
			Question string `json:"question"`
			Model    string `json:"model,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid json"})
			return
		}

		model := body.Model
		if model == "" {
			model = "qwen2.5:3b"
		}

		a := agent.NewDefaultAgent(model)
		answer, err := a.Chat(body.Question)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"answer": answer})
	})
	http.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {

		setCORS(w)

		writeJSON(w, 200, map[string]any{
			"tools": []map[string]string{
				{
					"name":        "search_code",
					"description": "Search repository using ripgrep",
				},
				{
					"name":        "open_file",
					"description": "Open file snippet by path and line range",
				},
			},
		})
	})

	// OpenAI-compatible models endpoint
	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}

		writeJSON(w, 200, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       "qwen2.5:3b",
					"object":   "model",
					"owned_by": "local",
				},
			},
		})
	})

	// OpenAI-compatible chat completions endpoint
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid json"})
			return
		}

		if len(req.Messages) == 0 {
			writeJSON(w, 400, map[string]string{"error": "messages are required"})
			return
		}

		// For MVP: use the last user message as the question.
		question := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				question = req.Messages[i].Content
				break
			}
		}
		if question == "" {
			question = req.Messages[len(req.Messages)-1].Content
		}

		model := req.Model
		if model == "" {
			model = "qwen2.5:3b"
		}

		a := agent.NewDefaultAgent(model)
		answer, err := a.Chat(question)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}

		resp := map[string]any{
			"id":      "chatcmpl-local",
			"object":  "chat.completion",
			"created": 0,
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": answer,
					},
					"finish_reason": "stop",
				},
			},
		}

		writeJSON(w, 200, resp)
	})

	log.Println("MCP server running on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
