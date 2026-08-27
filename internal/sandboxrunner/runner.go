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
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	agentsandbox "github.com/docker/docker-agent/pkg/sandbox"
	sbx "github.com/rumpl/go-sbx"
)

const AgentName = "daw-runner"

var invalidNameCharacter = regexp.MustCompile(`[^a-zA-Z0-9.+-]+`)

type Options struct {
	Workspace            string
	AdditionalWorkspaces []string
	Kit                  string
	// Template is a sandbox template image previously baked with EnsureTemplate.
	// When set, the staged kit omits the runner binary because it is already in
	// the image.
	Template string
	// PluginDir is mounted at the same absolute path inside the sandbox. The
	// runner discovers plugin MCP declarations there, just like dawui does on
	// the host. Empty disables host plugin mounting.
	PluginDir string
	Name      string
	CPUs      int
	Memory    string
	// SessionStoreToken authenticates reverse store RPC carried over stdio.
	SessionStoreToken string
	// ModelsGateway is the host's current models gateway. Docker gateways add
	// docker-agent's login mixin kit when the sandbox is created; all gateways
	// are added to the sandbox network policy before the runner starts.
	ModelsGateway string
	// SkipRunner materializes the kit without starting the process. It is used
	// only while baking a template, before the host store bridge exists.
	SkipRunner bool
}

type Runner struct {
	Name            string
	Token           string
	GatewayAuthHost string
	Process         *sbx.Process
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

// Start stages per-sandbox configuration, creates or resumes the sandbox, and
// opens the runner's long-lived sbx-exec stdio process. The sbx run command
// remains the authority for kit composition, credentials, policy, and sandbox
// lifecycle; no runner port is published.
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
	if !options.SkipRunner && strings.TrimSpace(options.SessionStoreToken) == "" {
		return Runner{}, errors.New("sandbox runner: host session store token is required")
	}
	workspaces := []string{workspace}
	for _, candidate := range options.AdditionalWorkspaces {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		extra, extraErr := existingDirectory(candidate)
		if extraErr != nil {
			return Runner{}, fmt.Errorf("sandbox runner: additional workspace: %w", extraErr)
		}
		alreadyMounted := false
		for _, mounted := range workspaces {
			if within(mounted, extra) {
				alreadyMounted = true
				break
			}
		}
		if !alreadyMounted {
			workspaces = append(workspaces, extra)
		}
	}
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
	stagedKit, cleanup, err := stageKit(kit, token, strings.TrimSpace(options.Template) == "")
	if err != nil {
		return Runner{}, err
	}
	defer cleanup()
	loginKit, err := agentsandbox.LoginKit(strings.TrimSpace(options.ModelsGateway))
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: create Docker gateway login kit: %w", err)
	}
	gatewayAuthHost := ""
	kits := []string{stagedKit}
	if loginKit != "" {
		kits = append(kits, loginKit)
		gatewayAuthHost = filepath.Base(loginKit)
	}

	runOptions := sbx.RunOptions{
		SandboxOptions: sbx.SandboxOptions{
			Agent: AgentName, Workspaces: workspaces, Name: name,
			Kits: kits, Template: strings.TrimSpace(options.Template),
		},
		Detached: true,
	}
	if runOptions.Template == "" {
		runOptions.CPUs = options.CPUs
		runOptions.Memory = options.Memory
	}
	// Existing sandboxes must be reattached by name alone; sbx rejects new
	// workspaces, templates, and kits on that path. The stable token allows the
	// host adapter to reconnect without recreating the VM.
	if _, portsErr := client.Ports(ctx, name); portsErr == nil {
		runOptions.SandboxOptions = sbx.SandboxOptions{Name: name}
	}
	err = client.Run(ctx, runOptions)
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: start %q: %w", name, err)
	}
	allowGatewayHost(ctx, client, name, options.ModelsGateway)
	if options.SkipRunner {
		return Runner{Name: name, Token: token, GatewayAuthHost: gatewayAuthHost}, nil
	}
	// Start the runner once through the normal exec path after sbx run completes.
	// setup.startup is deliberately not used because that launch context can see
	// proxy-managed placeholder keys before the credential proxy is attached.
	process, err := startRunner(context.WithoutCancel(ctx), client, name, options.SessionStoreToken)
	if err != nil {
		return Runner{}, err
	}
	return Runner{Name: name, Token: token, GatewayAuthHost: gatewayAuthHost, Process: process}, nil
}

// allowGatewayHost opens only the configured gateway authority in the
// sandbox's default-deny proxy. Credential injection remains restricted by the
// login kit to the exact trusted Docker hostname.
func allowGatewayHost(ctx context.Context, client *sbx.Client, name, rawURL string) {
	if strings.TrimSpace(rawURL) == "" {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(u.Host, ", \t") {
		slog.WarnContext(ctx, "models gateway host cannot be allowed in sandbox", "sandbox", name, "gateway", rawURL)
		return
	}
	if _, err := client.Command(ctx, "policy", "allow", "network", "--sandbox", name, u.Host); err != nil {
		// Older sandbox backends may not expose mutable policy. Let the runner
		// continue: its inherited policy may already allow this endpoint.
		slog.WarnContext(ctx, "allow models gateway in sandbox", "sandbox", name, "host", u.Host, "error", err)
	}
}

func startRunner(ctx context.Context, client *sbx.Client, name, storeToken string) (*sbx.Process, error) {
	command := `
set -eu
pid_file=/home/agent/.cagent/daw-runner/runner.pid
if [ -s "$pid_file" ]; then
  pid=$(cat "$pid_file")
  if kill -0 "$pid" 2>/dev/null; then
    # Existing sandboxes made by older kits may still auto-start the runner.
    # Replace that process so it also receives the post-initialization exec
    # credential context, but do not wait at all on new sandboxes.
    kill "$pid" 2>/dev/null || true
    tries=0
    while kill -0 "$pid" 2>/dev/null && [ "$tries" -lt 40 ]; do
      sleep 0.05
      tries=$((tries + 1))
    done
    kill -9 "$pid" 2>/dev/null || true
  fi
fi
rm -f "$pid_file"
exec env DAW_SESSION_STORE_TOKEN=` + shellQuote(storeToken) + ` /home/agent/.local/bin/start-daw-runner
`
	process, err := client.ExecPipe(ctx, name, "sh", "-c", command)
	if err != nil {
		return nil, fmt.Errorf("sandbox runner: start %q after sandbox initialization: %w", name, err)
	}
	return process, nil
}

func stageKit(source, token string, includeRunner bool) (string, func(), error) {
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
	if includeRunner {
		if err := os.CopyFS(staged, os.DirFS(source)); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("sandbox runner: stage full kit: %w", err)
		}
	} else {
		// Session templates already contain the runner and static setup files.
		// Build a genuinely small kit rather than copying the large executable
		// into a temporary tree only to remove it again.
		if err := os.MkdirAll(staged, 0o755); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("sandbox runner: stage lightweight kit: %w", err)
		}
		spec, err := os.ReadFile(filepath.Join(source, "spec.yaml"))
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("sandbox runner: read kit spec: %w", err)
		}
		if err := os.WriteFile(filepath.Join(staged, "spec.yaml"), spec, 0o644); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("sandbox runner: stage kit spec: %w", err)
		}
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

// RemoveToken deletes the host-side bearer token after its sandbox has been
// removed rather than merely stopped.
func RemoveToken(name string) error {
	path, err := tokenPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func tokenPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cagent", "dawui", "sandbox-tokens", name), nil
}

func persistentToken(name string) (string, error) {
	path, err := tokenPath(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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
