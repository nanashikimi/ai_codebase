package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrepFile_Basic(t *testing.T) {
	tmp := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	path := filepath.Join("cmd", "mcp-server", "main.go")
	mustWriteFileGrep(t, path, `package main

func main() {
	println("hello")
	println("world")
	println("hello again")
}
`)

	resp, err := GrepFile(GrepFileRequest{
		Path:  path,
		Query: "hello",
	})
	if err != nil {
		t.Fatalf("GrepFile returned error: %v", err)
	}

	if resp.Path != filepath.ToSlash(path) && resp.Path != path {
		t.Fatalf("unexpected path: %q", resp.Path)
	}

	if len(resp.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(resp.Hits))
	}

	if resp.Hits[0].Line != 4 {
		t.Fatalf("expected first hit on line 4, got %d", resp.Hits[0].Line)
	}
}

func TestGrepFile_MaxResults(t *testing.T) {
	tmp := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	path := "file.txt"
	mustWriteFileGrep(t, path, "a\na\na\n")

	resp, err := GrepFile(GrepFileRequest{
		Path:       path,
		Query:      "a",
		MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("GrepFile returned error: %v", err)
	}

	if len(resp.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(resp.Hits))
	}
}

func TestGrepFile_RejectsAbsolutePath(t *testing.T) {
	tmp := t.TempDir()

	_, err := GrepFile(GrepFileRequest{
		Path:  filepath.Join(tmp, "file.txt"),
		Query: "x",
	})
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestGrepFile_RejectsTraversal(t *testing.T) {
	_, err := GrepFile(GrepFileRequest{
		Path:  "../secret.txt",
		Query: "x",
	})
	if err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestGrepFile_RejectsEmptyQuery(t *testing.T) {
	_, err := GrepFile(GrepFileRequest{
		Path: "file.txt",
	})
	if err == nil {
		t.Fatal("expected query error")
	}
}

func mustWriteFileGrep(t *testing.T, path string, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) failed: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) failed: %v", path, err)
	}
}
