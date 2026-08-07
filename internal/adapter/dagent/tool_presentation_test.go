package dagent

import (
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
)

func presentationCall(name, arguments string) tools.ToolCall {
	return tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: arguments}}
}

func TestPresentationArgsForDefaultTools(t *testing.T) {
	t.Run("shell", func(t *testing.T) {
		got := presentationArgs(presentationCall("shell", `{"cmd":"npm test","cwd":"web","timeout":60}`))
		if got["cmd"] != "npm test" || got["cwd"] != "web" || got["timeout"] != float64(60) {
			t.Fatalf("unexpected shell arguments: %#v", got)
		}
	})

	t.Run("write includes a bounded content preview", func(t *testing.T) {
		got := presentationArgs(presentationCall("write_file", `{"path":"large.txt","content":"one\ntwo"}`))
		if got["contentPreview"] != "one\ntwo" {
			t.Fatalf("write preview missing: %#v", got)
		}
		if got["contentBytes"] != 7 || got["contentLines"] != 2 {
			t.Fatalf("unexpected write summary: %#v", got)
		}

		large := strings.Repeat("x", maxWritePreview+100)
		got = presentationArgs(presentationCall("write_file", `{"path":"large.txt","content":"`+large+`"}`))
		if len(got["contentPreview"].(string)) > maxWritePreview+len("…") || got["contentTruncated"] != true {
			t.Fatalf("large write preview was not bounded: %#v", got)
		}
	})

	t.Run("edit previews are bounded", func(t *testing.T) {
		payload := `{"path":"app.go","edits":[{"oldText":"` + strings.Repeat("a", maxEditPreview*2) + `","newText":"b"}]}`
		got := presentationArgs(presentationCall("edit_file", payload))
		edits, ok := got["edits"].([]map[string]any)
		if !ok || len(edits) != 1 {
			t.Fatalf("unexpected edit summary: %#v", got)
		}
		if old := edits[0]["oldText"].(string); len(old) > maxEditPreview+len("…") {
			t.Fatalf("edit preview was not bounded: %d", len(old))
		}
	})
}
