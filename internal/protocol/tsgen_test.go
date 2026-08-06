package protocol_test

import (
	"os"
	"testing"

	"github.com/rumpl/daw/internal/protocol"
)

// TestTypeScriptInSync is the anti-drift check: the checked-in TS mirror must
// equal the generator's output for the current Go types.
func TestTypeScriptInSync(t *testing.T) {
	const path = "../../web/src/protocol.gen.ts"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `make generate`)", path, err)
	}
	want := protocol.GenerateTypeScript()
	if string(got) != want {
		t.Fatalf("%s is out of date with internal/protocol; run `make generate`", path)
	}
}
