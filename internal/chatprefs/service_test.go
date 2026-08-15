package chatprefs

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDisabledToolsAreGlobal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	service := New(path, slog.Default())
	if err := service.UpdateTools([]string{"shell", "read_file", "shell"}); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"", "session-a", "session-b"} {
		got := service.Get(sessionID).DisabledTools
		if len(got) != 2 || got[0] != "read_file" || got[1] != "shell" {
			t.Fatalf("Get(%q).DisabledTools = %v", sessionID, got)
		}
	}

	reloaded := New(path, slog.Default())
	got := reloaded.Get("another-session").DisabledTools
	if len(got) != 2 || got[0] != "read_file" || got[1] != "shell" {
		t.Fatalf("reloaded disabled tools = %v", got)
	}
}

func TestPerSessionDisabledToolsMigrateToGlobal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	contents := `{"version":1,"default":{"disabledTools":["shell"]},"sessions":{"one":{"disabledTools":["read_file"]}}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(path, slog.Default())
	got := service.Get("two").DisabledTools
	if len(got) != 2 || got[0] != "read_file" || got[1] != "shell" {
		t.Fatalf("migrated disabled tools = %v", got)
	}
}
