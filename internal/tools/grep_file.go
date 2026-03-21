package tools

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type GrepFileRequest struct {
	Path       string `json:"path"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type GrepFileHit struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type GrepFileResponse struct {
	Path string        `json:"path"`
	Hits []GrepFileHit `json:"hits"`
}

func GrepFile(req GrepFileRequest) (GrepFileResponse, error) {
	p := strings.TrimSpace(req.Path)
	if p == "" {
		return GrepFileResponse{}, errors.New("path is required")
	}
	if filepath.IsAbs(p) {
		return GrepFileResponse{}, errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") {
		return GrepFileResponse{}, errors.New("path traversal is not allowed")
	}

	q := strings.TrimSpace(req.Query)
	if q == "" {
		return GrepFileResponse{}, errors.New("query is required")
	}

	max := req.MaxResults
	if max <= 0 || max > 500 {
		max = 50
	}

	f, err := os.Open(clean)
	if err != nil {
		return GrepFileResponse{}, err
	}
	defer f.Close()

	hits := make([]GrepFileHit, 0, max)

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 2*1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if strings.Contains(line, q) {
			hits = append(hits, GrepFileHit{
				Line: lineNo,
				Text: line,
			})
			if len(hits) >= max {
				break
			}
		}
	}

	if err := sc.Err(); err != nil {
		return GrepFileResponse{}, err
	}

	return GrepFileResponse{
		Path: clean,
		Hits: hits,
	}, nil
}

