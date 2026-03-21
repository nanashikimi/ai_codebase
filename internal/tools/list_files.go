package tools

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type ListFilesRequest struct {
	Root       string `json:"root,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type ListFilesResponse struct {
	Files []string `json:"files"`
}

func ListFiles(req ListFilesRequest) (ListFilesResponse, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		root = "."
	}

	// Basic safety: disallow absolute paths and path traversal.
	if filepath.IsAbs(root) {
		return ListFilesResponse{}, errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(root)
	if strings.HasPrefix(clean, "..") {
		return ListFilesResponse{}, errors.New("path traversal is not allowed")
	}

	max := req.MaxResults
	if max <= 0 || max > 5000 {
		max = 200
	}

	files := make([]string, 0, max)

	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"dist":         true,
		"build":        true,
		".venv":        true,
	}

	err := filepath.WalkDir(clean, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name := d.Name()
		if d.IsDir() && skipDirs[name] {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		normalized := filepath.ToSlash(path)
		files = append(files, normalized)

		if len(files) >= max {
			// stop early once enough files collected
			return fs.SkipAll
		}

		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return ListFilesResponse{}, err
	}

	sort.Strings(files)

	return ListFilesResponse{Files: files}, nil
}
