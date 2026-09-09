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
	// PluginDir is retained for launcher compatibility and host-side plugin
	// discovery. Local MCP commands no longer run in the sandbox, so this path
	// is not mounted by the session adapter.
	PluginDir string
	Name      string
	CPUs      int
	Memory    string
	// Reuse identifies a sandbox already owned by a persisted session. Existing
	// sandboxes must be reattached by name alone; new sessions can skip the
	// extra `sbx ports` discovery subprocess.
	Reuse bool
	// SessionStoreToken authenticates reverse store RPC carried over stdio.
	SessionStoreToken string
	// ModelsGateway is the host's current models gateway. Docker gateways add
	// docker-agent's login mixin kit when the sandbox is created; all gateways
	// are added to the sandbox network policy before the runner starts.
	ModelsGateway string
	// Progress receives lifecycle milestones suitable for a user-facing status
	// display. Callbacks must return quickly.
	Progress func(phase, message string)
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

func progress(options Options, phase, message string) {
	if options.Progress != nil {
		options.Progress(phase, message)
	}
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
	kit := strings.TrimSpace(options.Kit)
	if kit == "" {
		return Runner{}, errors.New("sandbox runner: published kit reference is required")
	}
	if strings.TrimSpace(options.SessionStoreToken) == "" {
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
	pluginDir := strings.TrimSpace(options.PluginDir)
	if pluginDir != "" {
		pluginDir, err = existingDirectory(pluginDir)
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
	loginKit, err := agentsandbox.LoginKit(strings.TrimSpace(options.ModelsGateway))
	if err != nil {
		return Runner{}, fmt.Errorf("sandbox runner: create Docker gateway login kit: %w", err)
	}
	gatewayAuthHost := ""
	kits := []string{}
	if loginKit != "" {
		kits = append(kits, loginKit)
		gatewayAuthHost = filepath.Base(loginKit)
	}

	runOptions := sbx.RunOptions{
		Agent: kit, Workspaces: workspaces, Name: name,
		Kits: kits, CPUs: options.CPUs, Memory: options.Memory, Detached: true,
	}
	if options.Reuse {
		// sbx rejects workspaces, kits, and resource options when reattaching.
		runOptions.SandboxOptions = sbx.SandboxOptions{Name: name}
	}
	progress(options, "starting", "Starting Docker Sandbox…")
	if !options.Reuse {
		err = client.Run(ctx, runOptions)
		if err != nil {
			return Runner{}, fmt.Errorf("sandbox runner: start %q: %w", name, err)
		}
	}
	// `sbx exec` starts a stopped sandbox itself. A persisted session therefore
	// avoids a redundant `sbx run --name` subprocess and goes straight to the
	// long-lived runner transport.
	progress(options, "configuring", "Configuring sandbox network access…")
	// Policy mutation is a separate sbx/daemon round trip. It can overlap runner
	// process startup because bootstrap only uses the host store; Start still
	// waits for policy completion before returning, so the first model request
	// cannot race the allow rule.
	policyDone := make(chan struct{})
	go func() {
		defer close(policyDone)
		allowGatewayHost(ctx, client, name, options.ModelsGateway)
	}()
	// Start the runner once through the normal exec path after sbx run completes.
	// setup.startup is deliberately not used because that launch context can see
	// proxy-managed placeholder keys before the credential proxy is attached.
	progress(options, "runner", "Starting agent runner…")
	process, err := startRunner(context.WithoutCancel(ctx), client, name, workspace, token, options.SessionStoreToken)
	<-policyDone
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

func startRunner(ctx context.Context, client *sbx.Client, name, workspace, runnerToken, storeToken string) (*sbx.Process, error) {
	command := `exec env DAW_RUNNER_WORKSPACE=` + shellQuote(workspace) + ` DAW_RUNNER_TOKEN=` + shellQuote(runnerToken) + ` DAW_SESSION_STORE_TOKEN=` + shellQuote(storeToken) + ` /home/agent/.local/lib/daw-runner`
	process, err := client.ExecPipe(ctx, name, "sh", "-c", command)
	if err != nil {
		return nil, fmt.Errorf("sandbox runner: start %q after sandbox initialization: %w", name, err)
	}
	return process, nil
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
