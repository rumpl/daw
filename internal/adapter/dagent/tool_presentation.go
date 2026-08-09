package dagent

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/docker/docker-agent/pkg/tools"
)

const (
	maxArgumentString       = 4000
	maxArgumentPaths        = 40
	maxEditPreviews         = 8
	maxEditPreview          = 1200
	maxWritePreview         = 16 * 1024
	maxCustomArgumentBytes  = 16 * 1024
	maxCustomArgumentItems  = 40
	maxCustomArgumentDepth  = 4
	maxCustomArgumentKeyLen = 200
)

// presentationArgs returns a bounded view of tool arguments for dashboard
// renderers. Built-in tools get purpose-built projections; custom tools get a
// generic, recursively bounded projection so plugin renderers do not require
// plugin-specific allowlists in core.
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
	default:
		return boundedCustomArguments(raw)
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func boundedCustomArguments(raw map[string]any) map[string]any {
	budget := maxCustomArgumentBytes
	value, ok := boundedCustomValue(raw, 0, &budget)
	if !ok {
		return nil
	}
	result, _ := value.(map[string]any)
	return result
}

func boundedCustomValue(value any, depth int, budget *int) (any, bool) {
	if depth > maxCustomArgumentDepth || *budget <= 0 {
		return nil, false
	}
	switch typed := value.(type) {
	case string:
		limit := min(maxArgumentString, *budget)
		text := truncateMultiline(typed, limit)
		*budget -= min(len(text), *budget)
		return text, true
	case nil, bool, float64:
		return typed, true
	case []any:
		out := make([]any, 0, min(len(typed), maxCustomArgumentItems))
		for _, item := range typed[:min(len(typed), maxCustomArgumentItems)] {
			clean, ok := boundedCustomValue(item, depth+1, budget)
			if ok {
				out = append(out, clean)
			}
			if *budget <= 0 {
				break
			}
		}
		return out, true
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, min(len(keys), maxCustomArgumentItems))
		for _, key := range keys[:min(len(keys), maxCustomArgumentItems)] {
			cleanKey := truncateMultiline(key, maxCustomArgumentKeyLen)
			if len(cleanKey) > *budget {
				break
			}
			*budget -= len(cleanKey)
			clean, ok := boundedCustomValue(typed[key], depth+1, budget)
			if ok {
				out[cleanKey] = clean
			}
			if *budget <= 0 {
				break
			}
		}
		return out, true
	default:
		return nil, false
	}
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
