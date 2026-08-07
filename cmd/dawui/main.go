// Command dawui runs the docker-agent web dashboard.
//
// It binds to the literal loopback host 127.0.0.1 and serves both the API and
// the embedded frontend from a single process. 127.0.0.1 is the security
// boundary; there is deliberately no HOST override.
package main

import (
	"context"
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
	slog.SetDefault(log)

	port, err := resolvePort(os.Getenv("PORT"))
	if err != nil {
		return err
	}

	guard, _, err := pathsec.NewGuard(pathsec.HomeRoots())
	if err != nil {
		return fmt.Errorf("the home directory is not usable as a workspace root: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ad adapter.Adapter
	fakeAdapter := os.Getenv("DAWUI_FAKE_ADAPTER") == "1"
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
	} else {
		real, err := dagent.New(ctx, dagent.Config{Logger: log, SessionDB: os.Getenv("DAWUI_SESSION_DB")})
		if err != nil {
			return fmt.Errorf("docker-agent could not be initialized: %w", err)
		}
		ad = real
	}

	// The real dashboard keeps its project MRU and chat control preferences
	// beside docker-agent's session data. Tests using the fake adapter stay
	// isolated unless they explicitly provide file paths.
	workspaceHistoryFile := strings.TrimSpace(os.Getenv("DAWUI_WORKSPACE_HISTORY_FILE"))
	chatPreferencesFile := strings.TrimSpace(os.Getenv("DAWUI_CHAT_PREFERENCES_FILE"))
	if (workspaceHistoryFile == "" || chatPreferencesFile == "") && !fakeAdapter {
		info, err := ad.Info(ctx)
		if err != nil {
			return fmt.Errorf("docker-agent could not report its data directory: %w", err)
		}
		if strings.TrimSpace(info.DataDir) == "" {
			return errors.New("docker-agent reported an empty data directory")
		}
		if workspaceHistoryFile == "" {
			workspaceHistoryFile = filepath.Join(info.DataDir, "dawui-workspaces.json")
		}
		if chatPreferencesFile == "" {
			chatPreferencesFile = filepath.Join(info.DataDir, "dawui-chat-preferences.json")
		}
	}

	pluginDir := strings.TrimSpace(os.Getenv("DAWUI_PLUGIN_DIR"))
	if pluginDir == "" {
		if fakeAdapter {
			pluginDir = filepath.Join(os.TempDir(), "dawui-fake-plugins")
		} else {
			info, err := ad.Info(ctx)
			if err != nil {
				return fmt.Errorf("docker-agent could not report its data directory: %w", err)
			}
			pluginDir = filepath.Join(info.DataDir, "dawui", "plugins")
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

	srv := httpapi.New(httpapi.Options{
		Adapter:              ad,
		Guard:                guard,
		AppVersion:           appVersion,
		TailscaleHosts:       splitList(os.Getenv("TAILSCALE_HOSTNAMES")),
		AllowedTSUsers:       splitList(os.Getenv("ALLOWED_TAILSCALE_USERS")),
		Static:               webassets.Handler(),
		Logger:               log,
		WorkspaceHistoryFile: workspaceHistoryFile,
		ChatPreferencesFile:  chatPreferencesFile,
		PluginDir:            pluginDir,
	})

	addr := net.JoinHostPort(bindHost, strconv.Itoa(port))
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("port %d on %s is already in use (EADDRINUSE). "+
				"Stop the other process or set PORT to a free port between 1024 and 65535", port, bindHost)
		}
		return fmt.Errorf("listening on %s: %w", addr, err)
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
	fmt.Printf("docker-agent dashboard listening on http://%s\n", addr)
	fmt.Printf("  workspace directory: %s\n", strings.Join(guard.Roots(), ", "))
	fmt.Printf("  no sandbox: tools run on this host as %s\n", currentUser())
	fmt.Printf("  global plugins: %s\n", pluginDir)
	fmt.Println("  all chats are autonomous: EVERY tool call is auto-approved")

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

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
	return httpServer.Shutdown(shutdownCtx)
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
