package dagent

import (
	"encoding/json"
	"strings"

	"github.com/docker/docker-agent/pkg/tools"
)

const (
	maxArgumentString = 4000
	maxArgumentPaths  = 40
	maxEditPreviews   = 8
	maxEditPreview    = 1200
	maxWritePreview   = 16 * 1024
)

// presentationArgs returns only the argument data the dashboard needs to
// render docker-agent's built-in filesystem and shell tools. File writes and
// edits include bounded content previews in addition to counts, so snapshots
// remain useful without echoing arbitrarily large model-generated payloads
// into every SSE event.
func presentationArgs(tc tools.ToolCall) map[string]any {
	var raw map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &raw); err != nil {
		return nil
	}

	out := map[string]any{}
	copyString := func(key string) {
		if value, ok := raw[key].(string); ok && value != "" {
			out[key] = truncateMultiline(value, maxArgumentString)
		}
	}
	copyValue := func(key string) {
		if value, ok := raw[key]; ok {
			out[key] = value
		}
	}
	copyPaths := func() {
		values, ok := raw["paths"].([]any)
		if !ok {
			return
		}
		paths := make([]string, 0, min(len(values), maxArgumentPaths))
		for _, value := range values {
			path, ok := value.(string)
			if !ok {
				continue
			}
			paths = append(paths, truncateMultiline(path, 500))
			if len(paths) == maxArgumentPaths {
				break
			}
		}
		out["paths"] = paths
		if len(values) > len(paths) {
			out["pathsTruncated"] = len(values) - len(paths)
		}
	}

	switch tc.Function.Name {
	case "shell":
		copyString("cmd")
		if _, ok := out["cmd"]; !ok { // accepted alias in docker-agent
			copyString("command")
		}
		copyString("cwd")
		copyValue("timeout")
	case "directory_tree", "list_directory":
		copyString("path")
	case "read_file":
		copyString("path")
		copyValue("line")
		copyValue("limit")
	case "read_multiple_files":
		copyPaths()
		copyValue("json")
	case "search_files_content":
		copyString("path")
		copyString("query")
		copyValue("is_regex")
		if patterns, ok := raw["excludePatterns"].([]any); ok {
			excludes := make([]string, 0, min(len(patterns), maxArgumentPaths))
			for _, value := range patterns {
				if pattern, ok := value.(string); ok {
					excludes = append(excludes, truncateMultiline(pattern, 500))
				}
				if len(excludes) == maxArgumentPaths {
					break
				}
			}
			out["excludePatterns"] = excludes
		}
	case "write_file":
		copyString("path")
		if content, ok := raw["content"].(string); ok {
			out["contentBytes"] = len(content)
			out["contentLines"] = lineCount(content)
			out["contentPreview"] = truncateMultiline(content, maxWritePreview)
			if len(content) > maxWritePreview {
				out["contentTruncated"] = true
			}
		}
	case "edit_file":
		copyString("path")
		if edits, ok := raw["edits"].([]any); ok {
			out["editCount"] = len(edits)
			previews := make([]map[string]any, 0, min(len(edits), maxEditPreviews))
			for _, value := range edits {
				edit, ok := value.(map[string]any)
				if !ok {
					continue
				}
				oldText, _ := edit["oldText"].(string)
				newText, _ := edit["newText"].(string)
				previews = append(previews, map[string]any{
					"oldText":      truncateMultiline(oldText, maxEditPreview),
					"newText":      truncateMultiline(newText, maxEditPreview),
					"removedLines": lineCount(oldText),
					"addedLines":   lineCount(newText),
				})
				if len(previews) == maxEditPreviews {
					break
				}
			}
			out["edits"] = previews
			if len(edits) > len(previews) {
				out["editsTruncated"] = len(edits) - len(previews)
			}
		}
	case "create_directory", "remove_directory":
		copyPaths()
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func truncateMultiline(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func lineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}
