// Command dawui runs the docker-agent web dashboard.
//
// By default it binds to the literal loopback host 127.0.0.1 and serves both
// the API and embedded frontend from one process. The Electron host instead
// sets DAWUI_SOCKET, making the same HTTP server listen only on a Unix domain
// socket. There is deliberately no configurable TCP host.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/adapter/dagent"
	"github.com/rumpl/daw/internal/adapter/fake"
	remoteadapter "github.com/rumpl/daw/internal/adapter/remote"
	"github.com/rumpl/daw/internal/httpapi"
	"github.com/rumpl/daw/internal/pathsec"
	"github.com/rumpl/daw/internal/webassets"
)

// bindHost is not configurable. Widening it would defeat the entire security
// model: everything else (CSRF, host allow-list, Tailscale Serve) assumes the
// listener is reachable only from this machine.
const bindHost = "127.0.0.1"

const defaultPort = 4788

// appVersion is overridden at build time with -ldflags.
var appVersion = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	logLevel := slog.LevelInfo
	if os.Getenv("DAWUI_DEBUG") != "" {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	// Plugin backends and per-chat MCP processes inherit this identifier. It
	// lets plugins isolate private IPC resources when a web server and Electron
	// (or multiple development servers) run at the same time.
	if err := os.Setenv("DAW_INSTANCE_ID", strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("setting dashboard instance id: %w", err)
	}
	// docker-agent logs through slog's process-global default. DAW passes its
	// own logger explicitly, so discard the global logger to keep SDK internals
	// out of the dashboard's output.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	socketPath := strings.TrimSpace(os.Getenv("DAWUI_SOCKET"))
	port := defaultPort
	var err error
	if socketPath == "" {
		port, err = resolvePort(os.Getenv("PORT"))
		if err != nil {
			return err
		}
	} else {
		socketPath, err = filepath.Abs(socketPath)
		if err != nil {
			return fmt.Errorf("invalid DAWUI_SOCKET: %w", err)
		}
	}

	workspaceRoots := pathsec.HomeRoots()
	if runnerWorkspace := strings.TrimSpace(os.Getenv("DAWUI_RUNNER_WORKSPACE")); runnerWorkspace != "" {
		workspaceRoots = []string{runnerWorkspace}
	}
	guard, _, err := pathsec.NewGuard(workspaceRoots)
	if err != nil {
		return fmt.Errorf("the home directory is not usable as a workspace root: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ad adapter.Adapter
	var mcpBridgeListener net.Listener
	var mcpBridgeToken string
	fakeAdapter := os.Getenv("DAWUI_FAKE_ADAPTER") == "1"
	runnerEndpoint := strings.TrimSpace(os.Getenv("DAWUI_RUNNER_URL"))
	sandboxed := runnerEndpoint != ""
	if fakeAdapter && sandboxed {
		return errors.New("DAWUI_FAKE_ADAPTER and DAWUI_RUNNER_URL cannot be combined")
	}
	if fakeAdapter {
		log.Warn("using the FAKE docker-agent adapter (DAWUI_FAKE_ADAPTER=1): no real agent will run")
		f := fake.New()
		if d := os.Getenv("DAWUI_FAKE_DELAY_MS"); d != "" {
			if ms, err := strconv.Atoi(d); err == nil {
				f.Delay = time.Duration(ms) * time.Millisecond
			}
		}
		f.Seed("seeded-session-1", "Earlier conversation", os.Getenv("DAWUI_FAKE_WORKSPACE"), nil)
		ad = f
	} else if sandboxed {
		if socketPath != "" {
			return errors.New("sandbox runner mode does not yet support the Electron Unix-socket transport")
		}
		mcpBridgeListener, err = (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("start sandbox MCP callback bridge: %w", err)
		}
		defer mcpBridgeListener.Close()
		mcpBridgeToken, err = randomToken()
		if err != nil {
			return fmt.Errorf("create sandbox MCP callback token: %w", err)
		}
		bridgePort := mcpBridgeListener.Addr().(*net.TCPAddr).Port
		remoteAdapter, err := remoteadapter.New(remoteadapter.Config{
			Endpoint:       runnerEndpoint,
			Token:          os.Getenv("DAWUI_RUNNER_TOKEN"),
			CallbackOrigin: "http://host.docker.internal:" + strconv.Itoa(bridgePort),
			CallbackToken:  mcpBridgeToken,
		})
		if err != nil {
			return err
		}
		ad = remoteAdapter
	} else {
		realAdapter, err := dagent.New(ctx, dagent.Config{Logger: log, SessionDB: os.Getenv("DAWUI_SESSION_DB")})
		if err != nil {
			return fmt.Errorf("docker-agent could not be initialized: %w", err)
		}
		ad = realAdapter
	}

	// Control-plane state and plugins stay on the host even when the adapter is
	// remote. Only the runner's session/runtime state belongs to the sandbox.
	controlDataDir := ""
	if !fakeAdapter {
		if sandboxed {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve host data directory: %w", err)
			}
			controlDataDir = filepath.Join(home, ".cagent")
		} else {
			info, err := ad.Info(ctx)
			if err != nil {
				return fmt.Errorf("docker-agent could not report its data directory: %w", err)
			}
			controlDataDir = strings.TrimSpace(info.DataDir)
		}
		if controlDataDir == "" {
			return errors.New("docker-agent reported an empty data directory")
		}
	}

	workspaceHistoryFile := strings.TrimSpace(os.Getenv("DAWUI_WORKSPACE_HISTORY_FILE"))
	chatPreferencesFile := strings.TrimSpace(os.Getenv("DAWUI_CHAT_PREFERENCES_FILE"))
	if workspaceHistoryFile == "" && !fakeAdapter {
		workspaceHistoryFile = filepath.Join(controlDataDir, "dawui-workspaces.json")
	}
	if chatPreferencesFile == "" && !fakeAdapter {
		chatPreferencesFile = filepath.Join(controlDataDir, "dawui-chat-preferences.json")
	}

	pluginDir := strings.TrimSpace(os.Getenv("DAWUI_PLUGIN_DIR"))
	if pluginDir == "" {
		if fakeAdapter {
			pluginDir = filepath.Join(os.TempDir(), "dawui-fake-plugins")
		} else {
			pluginDir = filepath.Join(controlDataDir, "dawui", "plugins")
		}
	}
	pluginDir, err = absoluteUserPath(pluginDir)
	if err != nil {
		return fmt.Errorf("invalid DAWUI_PLUGIN_DIR: %w", err)
	}
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return fmt.Errorf("creating plugin directory: %w", err)
	}
	// The SDK-built coding agent inherits this exact resolved location, so it
	// never has to guess where global plugins belong.
	if err := os.Setenv("DAWUI_PLUGIN_DIR", pluginDir); err != nil {
		return fmt.Errorf("exporting plugin directory: %w", err)
	}

	pluginDataDir := strings.TrimSpace(os.Getenv("DAWUI_PLUGIN_DATA_DIR"))
	if pluginDataDir == "" {
		pluginDataDir = filepath.Join(filepath.Dir(pluginDir), "plugin-data")
	}
	pluginDataDir, err = absoluteUserPath(pluginDataDir)
	if err != nil {
		return fmt.Errorf("invalid DAWUI_PLUGIN_DATA_DIR: %w", err)
	}
	if err := os.MkdirAll(pluginDataDir, 0o700); err != nil {
		return fmt.Errorf("creating plugin data directory: %w", err)
	}
	listenNetwork := "tcp4"
	listenAddress := net.JoinHostPort(bindHost, strconv.Itoa(port))
	pluginAPIOrigin := "http://" + listenAddress
	if socketPath != "" {
		listenNetwork = "unix"
		listenAddress = socketPath
		pluginAPIOrigin = "http://localhost"
	}
	log.Info("dashboard starting", "version", appVersion, "network", listenNetwork, "address", listenAddress, "plugin_directory", pluginDir, "plugin_data_directory", pluginDataDir, "fake_adapter", fakeAdapter, "sandboxed", sandboxed)
	tailscaleHosts := splitList(os.Getenv("TAILSCALE_HOSTNAMES"))

	srv := httpapi.New(httpapi.Options{
		Adapter:              ad,
		Guard:                guard,
		AppVersion:           appVersion,
		Sandboxed:            sandboxed,
		TailscaleHosts:       tailscaleHosts,
		AllowedTSUsers:       splitList(os.Getenv("ALLOWED_TAILSCALE_USERS")),
		Static:               webassets.Handler(),
		Logger:               log,
		WorkspaceHistoryFile: workspaceHistoryFile,
		ChatPreferencesFile:  chatPreferencesFile,
		PluginDir:            pluginDir,
		PluginAPIOrigin:      pluginAPIOrigin,
		PluginAPISocket:      socketPath,
		PluginDataDir:        pluginDataDir,
	})

	var ln net.Listener
	if socketPath != "" {
		ln, err = listenUnix(ctx, socketPath)
	} else {
		ln, err = (&net.ListenConfig{}).Listen(ctx, listenNetwork, listenAddress)
	}
	if err != nil {
		if isAddrInUse(err) {
			if socketPath != "" {
				return fmt.Errorf("unix socket %s is already in use (EADDRINUSE)", socketPath)
			}
			return fmt.Errorf("port %d on %s is already in use (EADDRINUSE). "+
				"Stop the other process or set PORT to a free port between 1024 and 65535", port, bindHost)
		}
		return fmt.Errorf("listening on %s: %w", listenAddress, err)
	}
	defer ln.Close()
	if socketPath != "" {
		defer os.Remove(socketPath)
	}

	httpServer := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams are long-lived by design.
		IdleTimeout: 120 * time.Second,
	}

	if !webassets.Available() {
		log.Warn("frontend assets are not built into this binary; run `make build`")
	}
	if socketPath != "" {
		fmt.Printf("docker-agent dashboard listening on unix://%s\n", socketPath)
	} else {
		fmt.Printf("docker-agent dashboard listening on http://%s\n", listenAddress)
	}
	fmt.Printf("  workspace directory: %s\n", strings.Join(guard.Roots(), ", "))
	if sandboxed {
		fmt.Printf("  sandbox runner: %s\n", runnerEndpoint)
	} else {
		fmt.Printf("  no sandbox: tools run on this host as %s\n", currentUser())
	}
	fmt.Printf("  global plugins: %s\n", pluginDir)
	fmt.Println("  all chats are autonomous: EVERY tool call is auto-approved")

	errCh := make(chan error, 2)
	go func() { errCh <- httpServer.Serve(ln) }()
	var mcpBridgeServer *http.Server
	if mcpBridgeListener != nil {
		mcpBridgeServer = &http.Server{
			Handler: srv.MCPBridge(mcpBridgeToken), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		}
		go func() { errCh <- mcpBridgeServer.Serve(mcpBridgeListener) }()
	}

	select {
	case <-ctx.Done():
		fmt.Println("\nshutting down…")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	if mcpBridgeServer != nil {
		_ = mcpBridgeServer.Shutdown(shutdownCtx)
	}
	return httpServer.Shutdown(shutdownCtx)
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func absoluteUserPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "the current user"
}

// resolvePort validates the PORT override. Only 1024-65535 is accepted so the
// server never needs privileges.
func resolvePort(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultPort, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("PORT must be a number between 1024 and 65535, got %q", v)
	}
	if n < 1024 || n > 65535 {
		return 0, fmt.Errorf("PORT must be between 1024 and 65535, got %d", n)
	}
	return n, nil
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// listenUnix removes an abandoned socket left after an unclean exit, but never
// unlinks a live listener or an ordinary file. The socket is owner-only because
// it carries the complete local dashboard API.
func listenUnix(ctx context.Context, path string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("DAWUI_SOCKET exists and is not a socket: %s", path)
		}
		dialer := &net.Dialer{Timeout: 200 * time.Millisecond}
		conn, dialErr := dialer.DialContext(ctx, "unix", path)
		if dialErr == nil {
			_ = conn.Close()
			return nil, syscall.EADDRINUSE
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("removing stale Unix socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checking Unix socket: %w", err)
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("securing Unix socket: %w", err)
	}
	return ln, nil
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
