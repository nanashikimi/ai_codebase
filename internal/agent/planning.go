package agent

import "strings"

// for offtop questions(irrelevant ones), in order not to make a repo search by such ones
// it s a temporal unique-case defense, based on agent's behavior
// Plan: add a special prompt for such situations
func isOffTopicNonRepoQuestion(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return false
	}
	if strings.Contains(q, "kid") && strings.Contains(q, "art") {
		return true
	}
	if (strings.Contains(q, "kids'") || strings.Contains(q, "kids’")) && strings.Contains(q, "art") {
		return true
	}
	return false
}

func extraToolHint(question string) string {
	q := strings.ToLower(strings.TrimSpace(question))
	if strings.Contains(q, "what files") ||
		strings.Contains(q, "list files") ||
		strings.Contains(q, "show files") ||
		strings.Contains(q, "files are in") ||
		strings.Contains(q, "directory") {
		return "This looks like a directory structure question. Prefer list_files."
	}
	if strings.Contains(q, ".go") ||
		strings.Contains(q, ".py") ||
		strings.Contains(q, ".js") ||
		strings.Contains(q, ".ts") ||
		strings.Contains(q, ".java") ||
		strings.Contains(q, ".cpp") ||
		strings.Contains(q, ".c") {
		return "This question mentions a specific file. Prefer grep_file for searching inside that file."
	}
	return ""
}
