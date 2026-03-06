package tools

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type OpenFileRequest struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"` // 1-based
	EndLine   int    `json:"end_line,omitempty"`   // 1-based inclusive
	MaxChars  int    `json:"max_chars,omitempty"`
}

type OpenFileResponse struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func OpenFile(req OpenFileRequest) (OpenFileResponse, error) {
	p := strings.TrimSpace(req.Path)
	if p == "" {
		return OpenFileResponse{}, errors.New("path is required")
	}
	// Basic safety: disallow absolute paths and path traversal.
	if filepath.IsAbs(p) {
		return OpenFileResponse{}, errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") {
		return OpenFileResponse{}, errors.New("path traversal is not allowed")
	}

	start := req.StartLine
	end := req.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 {
		end = start + 200 // default window
	}
	if end < start {
		return OpenFileResponse{}, errors.New("end_line must be >= start_line")
	}

	maxChars := req.MaxChars
	if maxChars <= 0 || maxChars > 200_000 {
		maxChars = 40_000
	}

	f, err := os.Open(clean)
	if err != nil {
		return OpenFileResponse{}, err
	}
	defer f.Close()

	var b strings.Builder
	sc := bufio.NewScanner(f)
	// allow long lines
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 2*1024*1024)

	lineNo := 0
	truncated := false

	for sc.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo > end {
			break
		}

		line := sc.Text()
		// Keep original lines with \n
		if b.Len()+len(line)+1 > maxChars {
			truncated = true
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	if err := sc.Err(); err != nil {
		return OpenFileResponse{}, err
	}

	actualEnd := end
	if lineNo < end {
		actualEnd = lineNo
	}

	return OpenFileResponse{
		Path:      clean,
		StartLine: start,
		EndLine:   actualEnd,
		Content:   b.String(),
		Truncated: truncated,
	}, nil
}
