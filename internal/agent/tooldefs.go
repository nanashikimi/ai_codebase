package agent

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

func makeListFilesToolDef() ToolDef {
	var t ToolDef
	t.Type = "function"
	t.Function.Name = "list_files"
	t.Function.Description = "List files in the repository or in a subdirectory."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"root":        map[string]any{"type": "string", "description": "Optional root directory relative to repo root."},
			"max_results": map[string]any{"type": "integer", "description": "Max number of files to return."},
		},
	}
	return t
}

func makeGrepFileToolDef() ToolDef {
	var t ToolDef
	t.Type = "function"
	t.Function.Name = "grep_file"
	t.Function.Description = "Search for a string inside a single file and return matching lines with line numbers."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path relative to repo root.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Substring to search for.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Max number of matches to return.",
			},
		},
		"required": []string{"path", "query"},
	}
	return t
}
