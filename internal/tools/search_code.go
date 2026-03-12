package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type SearchCodeRequest struct {
	Query string `json:"query"`
	// Root is optional; if empty, current working dir is used by the server.
	Root string `json:"root,omitempty"`
	// MaxResults limits results returned (soft limit).
	MaxResults int `json:"max_results,omitempty"`
}

type SearchHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type SearchCodeResponse struct {
	Hits []SearchHit `json:"hits"`
}

func SearchCode(req SearchCodeRequest) (SearchCodeResponse, error) {
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return SearchCodeResponse{}, errors.New("query is required")
	}
	max := req.MaxResults
	if max <= 0 || max > 200 {
		max = 50
	}

	// rg --json gives stable machine-readable output.
	args := []string{
		"--json",
		"--no-heading",
		"--line-number",
		"--smart-case",
		"--hidden",
		"--glob", "!.git/*",
		"--glob", "!**/node_modules/*",
		"--glob", "!**/dist/*",
		"--glob", "!**/build/*",
		"--glob", "!**/.venv/*",
		"--glob", "!internal/agent/*",
		"--glob", "!prompts/*",
		"--glob", "!examples/*",
		"--glob", "!README.md",
		"--glob", "!scripts/*",
		q,
	}
	cmd := exec.Command("rg", args...)
	if strings.TrimSpace(req.Root) != "" {
		cmd.Dir = req.Root
	}

	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "rg", args...)
	if strings.TrimSpace(req.Root) != "" {
		cmd.Dir = req.Root
	}
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		// rg exit code 1 means "no matches" — not an error.
		if ctx.Err() != nil {
			return SearchCodeResponse{}, errors.New("search timed out")
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return SearchCodeResponse{Hits: []SearchHit{}}, nil
		}
		return SearchCodeResponse{}, errors.New("rg failed: " + strings.TrimSpace(errb.String()))
	}

	// Parse rg --json stream.
	type rgLine struct {
		Type string `json:"type"`
		Data struct {
			Path struct {
				Text string `json:"text"`
			} `json:"path"`
			LineNumber int `json:"line_number"`
			Lines      struct {
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"data"`
	}

	hits := make([]SearchHit, 0, max)
	dec := json.NewDecoder(&out)
	for dec.More() {
		var e rgLine
		if err := dec.Decode(&e); err != nil {
			break
		}
		if e.Type != "match" {
			continue
		}
		hits = append(hits, SearchHit{
			Path: e.Data.Path.Text,
			Line: e.Data.LineNumber,
			Text: strings.TrimRight(e.Data.Lines.Text, "\n"),
		})
		if len(hits) >= max {
			break
		}
	}

	return SearchCodeResponse{Hits: hits}, nil
}
