package dagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/paths"
)

func TestModelsGatewayUsesNativeDockerAgentConfig(t *testing.T) {
	configDir := t.TempDir()
	paths.SetConfigDir(configDir)
	t.Cleanup(func() { paths.SetConfigDir("") })

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: v1\nmodels_gateway: https://old.example.com\nfuture_setting: preserved\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{}
	if err := a.SetModelsGateway(context.Background(), " https://gateway.example.com/proxy/ "); err != nil {
		t.Fatal(err)
	}
	got, err := a.ModelsGateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://gateway.example.com/proxy" {
		t.Fatalf("gateway URL = %q", got)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if !strings.Contains(contents, "models_gateway: https://gateway.example.com/proxy") {
		t.Fatalf("updated gateway is absent from config:\n%s", contents)
	}
	if !strings.Contains(contents, "future_setting: preserved") {
		t.Fatalf("unrecognized Docker Agent config was dropped:\n%s", contents)
	}
}
