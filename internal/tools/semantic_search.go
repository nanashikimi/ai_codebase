package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type SemanticSearchRequest struct {
	Query string `json:"query"`
	Root  string `json:"root,omitempty"`
	TopK  int    `json:"top_k,omitempty"`
}

type SemanticSearchHit struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Preview   string  `json:"preview,omitempty"`
	Text      string  `json:"text,omitempty"`
}

type SemanticSearchResponse struct {
	Hits []SemanticSearchHit `json:"hits"`
}

type retrievalError struct { //errors in fastapi
	Error  string `json:"error"`
	Detail any    `json:"detail"`
}

func (e retrievalError) message() string { //visualize error message
	if strings.TrimSpace(e.Error) != "" {
		return e.Error
	}
	if e.Detail == nil {
		return ""
	}
	switch t := e.Detail.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func SemanticSearch(req SemanticSearchRequest) (SemanticSearchResponse, error) {
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return SemanticSearchResponse{}, errors.New("query is required")
	}
	topK := req.TopK
	if topK <= 0 || topK > 50 {
		topK = 8 // default
	}
	// Case: model requests too few results ==>  it can miss the relevant chunk
	// it means keeping small minimum to improve retrieval quality for "where is X"-type questions
	if topK < 5 {
		topK = 5 // else if it s natural number, but too small we used a value decresed comapring to default, but still sufficient
	}
	baseURL := strings.TrimSpace(os.Getenv("RETRIEVAL_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	client := &http.Client{Timeout: 60 * time.Second}
	searchOnce := func() (*http.Response, []byte, error) {
		body := map[string]any{
			"query": q,
			"top_k": topK,
			"root":  strings.TrimSpace(req.Root),
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/search", &buf) // no double slash
		if err != nil {
			return nil, nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r) //sending our request
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(resp.Body)
		return resp, raw.Bytes(), nil
	}

	resp, raw, err := searchOnce()
	if err != nil {
		return SemanticSearchResponse{}, err
	}
	// index missing (or root mismatch) ==> build it once, retry search after.
	if resp.StatusCode == 409 { // "Conflict"
		if err := buildIndex(client, baseURL, req.Root); err != nil {
			return SemanticSearchResponse{}, err
		}
		resp2, raw2, err2 := searchOnce() // retry search
		if err2 != nil {
			return SemanticSearchResponse{}, err2
		}
		if resp2.StatusCode >= 300 { //failured HTTP-code
			return SemanticSearchResponse{}, fmt.Errorf("retrieval service error: http %d: %s", resp2.StatusCode, strings.TrimSpace(string(raw2)))
		}
		var out SemanticSearchResponse
		if err := json.Unmarshal(raw2, &out); err != nil { //parsing response
			return SemanticSearchResponse{}, err
		}
		return out, nil
	}
	if resp.StatusCode >= 300 { //not conflict, but still failure
		var re retrievalError
		if err := json.Unmarshal(raw, &re); err == nil {
			if msg := strings.TrimSpace(re.message()); msg != "" {
				return SemanticSearchResponse{}, fmt.Errorf("retrieval service error: http %d: %s", resp.StatusCode, msg)
			}
		}
		return SemanticSearchResponse{}, fmt.Errorf("retrieval service error: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out SemanticSearchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return SemanticSearchResponse{}, err
	}
	return out, nil
}
func buildIndex(client *http.Client, baseURL string, root string) error {
	body := map[string]any{
		"root": strings.TrimSpace(root),
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/index", &buf)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r) //send request
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body) // and read response
	if resp.StatusCode >= 300 {
		return fmt.Errorf("index build failed: http %d: %s", resp.StatusCode, strings.TrimSpace(raw.String()))
	}
	return nil
}
