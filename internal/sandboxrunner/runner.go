// Package sandboxrunner creates and discovers the Docker Sandbox that hosts a
// dashboard runner.
package sandboxrunner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	sbx "github.com/rumpl/go-sbx"
)

const (
	AgentName     = "daw-runner"
	ContainerPort = 8080
)

var invalidNameCharacter = regexp.MustCompile(`[^a-zA-Z0-9.+-]+`)

type Options struct {
	Workspace string
	Kit       string
	// PluginDir is mounted at the same absolute path inside the sandbox. The
	// runner discovers plugin MCP declarations there, just like dawui does on
	// the host. Empty disables host plugin mounting.
	PluginDir string
	Name      string
	CPUs      int
	Memory    string
}

type Runner struct {
	Name     string
	Endpoint string
	Token    string
}

// DefaultName returns a stable sandbox name without putting an absolute host
// path in sbx's user-visible resources.
func DefaultName(workspace string) string {
	clean := filepath.Clean(workspace)
	base := strings.Trim(invalidNameCharacter.ReplaceAllString(filepath.Base(clean), "-"), "-.")
	if base == "" {
		base = "workspace"
	}
	sum := sha256.Sum256([]byte(clean))
	return "daw-" + base + "-" + hex.EncodeToString(sum[:4])
}

// Start validates the kit, creates the sandbox, and returns its loopback-only
// published API endpoint. The installed sbx CLI remains the authority for kit
// composition, credential bindings, policy, and sandbox creation.
func Start(ctx context.Context, client *sbx.Client, options Options) (Runner, error) {
	if client == nil {
		return Runner{}, errors.New("sandbox runner: nil sbx client")
	}
	workspace, err := existingDirectory(options.Workspace)
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: workspace: %w", err)
	}
	kit, err := existingDirectory(options.Kit)
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: kit: %w", err)
	}
	workspaces := []string{workspace}
	pluginDir := ""
	if strings.TrimSpace(options.PluginDir) != "" {
		pluginDir, err = existingDirectory(options.PluginDir)
		if err != nil {
			return Runner{}, fmt.Errorf("sandbox runner: plugin directory: %w", err)
		}
		if !within(workspace, pluginDir) {
			workspaces = append(workspaces, pluginDir)
		}
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = DefaultName(workspace)
	}
	token, err := persistentToken(name)
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: create authentication token: %w", err)
	}
	stagedKit, cleanup, err := stageKit(kit, token)
	if err != nil {
		return Runner{}, err
	}
	defer cleanup()
	if _, err := client.Kits().Validate(ctx, stagedKit); err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: validate staged kit: %w", err)
	}

	runOptions := sbx.RunOptions{
		SandboxOptions: sbx.SandboxOptions{
			Agent: AgentName, Workspaces: workspaces, Name: name,
			Kits: []string{stagedKit}, CPUs: options.CPUs, Memory: options.Memory,
			Publish: []string{strconv.Itoa(ContainerPort)},
		},
		Detached: true,
	}
	// Existing sandboxes must be reattached by name alone; sbx rejects new
	// workspaces and kits on that path. The stable token allows the host adapter
	// to reconnect without recreating the VM.
	reusing := false
	if _, portsErr := client.Ports(ctx, name); portsErr == nil {
		reusing = true
		runOptions.SandboxOptions = sbx.SandboxOptions{Name: name}
	}
	err = client.Run(ctx, runOptions)
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: start %q: %w", name, err)
	}
	if reusing {
		binary := filepath.Join(stagedKit, "files", "home", ".local", "lib", "daw-runner")
		if err := updateExistingRunner(ctx, client, name, binary); err != nil {
			return Runner{}, err
		}
	}
	endpoint, err := Endpoint(ctx, client, name)
	if err != nil {
		return Runner{}, err
	}
	return Runner{Name: name, Endpoint: endpoint, Token: token}, nil
}

// Endpoint asks sbx for the current host-side port allocation. Host ports are
// intentionally ephemeral, so they must never be inferred from the kit.
func Endpoint(ctx context.Context, client *sbx.Client, name string) (string, error) {
	if client == nil {
		return "", errors.New("sandbox runner: nil sbx client")
	}
	ports, err := client.Ports(ctx, name)
	if err != nil {
		return "", fmt.Errorf("sandbox runner: inspect ports for %q: %w", name, err)
	}
	for _, port := range ports {
		if port.SandboxPort == ContainerPort && port.Protocol == "tcp" && port.HostIP == "127.0.0.1" {
			return "http://" + net.JoinHostPort(port.HostIP, strconv.Itoa(port.HostPort)), nil
		}
	}
	return "", fmt.Errorf("sandbox runner: %q has no loopback publication for tcp/%d", name, ContainerPort)
}

func updateExistingRunner(ctx context.Context, client *sbx.Client, name, binary string) error {
	const temporary = "/home/agent/.cagent/daw-runner/daw-runner.update"
	if _, err := client.Command(ctx, "cp", binary, name+":"+temporary); err != nil {
		return fmt.Errorf("sandbox runner: copy runner update: %w", err)
	}
	const script = `
set -eu
pid_file=/home/agent/.cagent/daw-runner/runner.pid
if [ -s "$pid_file" ]; then
  pid=$(cat "$pid_file")
  kill "$pid" 2>/dev/null || true
  i=0
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 50 ]; do
    sleep 0.1
    i=$((i + 1))
  done
fi
install -m 0755 /home/agent/.cagent/daw-runner/daw-runner.update /home/agent/.local/lib/daw-runner
rm -f /home/agent/.cagent/daw-runner/daw-runner.update "$pid_file"
nohup /home/agent/.local/bin/start-daw-runner >/dev/null 2>&1 &
`
	if _, err := client.Command(ctx, "exec", name, "--", "sh", "-lc", script); err != nil {
		return fmt.Errorf("sandbox runner: install runner update: %w", err)
	}
	return nil
}

// PrimeCredentials sends harmless provider catalog requests through the
// sandbox's explicit HTTP proxy after sandbox setup has completed. Current sbx
// releases can otherwise let the first SDK request take the transparent bypass
// path, which forwards the literal proxy-managed sentinel and returns 401.
func PrimeCredentials(ctx context.Context, client *sbx.Client, name string) error {
	if client == nil {
		return errors.New("sandbox runner: nil sbx client")
	}
	const script = `
set +u
if [ "$OPENAI_API_KEY" = "proxy-managed" ]; then
  curl -sS --max-time 20 -H "Authorization: Bearer $OPENAI_API_KEY" https://api.openai.com/v1/models >/dev/null || true
fi
if [ "$ANTHROPIC_API_KEY" = "proxy-managed" ]; then
  curl -sS --max-time 20 -H "x-api-key: $ANTHROPIC_API_KEY" -H "anthropic-version: 2023-06-01" https://api.anthropic.com/v1/models >/dev/null || true
fi
exit 0
`
	if _, err := client.Command(ctx, "exec", name, "--", "sh", "-lc", script); err != nil {
		return fmt.Errorf("sandbox runner: prime credential proxy: %w", err)
	}
	return nil
}

// WaitReady waits for the runner process to accept API requests.
func WaitReady(ctx context.Context, endpoint, token string) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/v1/health", nil)
		if err != nil {
			return fmt.Errorf("sandbox runner: health request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("sandbox runner: waiting for %s: %w", endpoint, ctx.Err())
		case <-ticker.C:
		}
	}
}

func stageKit(source, token string) (string, func(), error) {
	binary := filepath.Join(source, "files", "home", ".local", "lib", "daw-runner")
	info, err := os.Stat(binary)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", func() {}, fmt.Errorf("sandbox runner: kit has no runner binary; run `make build-runner-kit`")
		}
		return "", func() {}, fmt.Errorf("sandbox runner: inspect kit runner binary: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", func() {}, fmt.Errorf("sandbox runner: kit runner binary is not executable")
	}

	parent, err := os.MkdirTemp("", "daw-runner-kit-")
	if err != nil {
		return "", func() {}, fmt.Errorf("sandbox runner: create staged kit: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(parent) }
	staged := filepath.Join(parent, AgentName)
	if err := os.CopyFS(staged, os.DirFS(source)); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("sandbox runner: stage kit: %w", err)
	}
	configDir := filepath.Join(staged, "files", "home", ".config", "daw")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("sandbox runner: stage runner configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "runner-token"), []byte(token+"\n"), 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("sandbox runner: stage runner token: %w", err)
	}
	return staged, cleanup, nil
}

func persistentToken(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cagent", "dawui", "sandbox-tokens")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if data, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) == 64 {
			if _, err := hex.DecodeString(token); err == nil {
				return token, nil
			}
		}
		return "", errors.New("stored runner token is invalid")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(value[:])
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func existingDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absolute)
	}
	return absolute, nil
}
