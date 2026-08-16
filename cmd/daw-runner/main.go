// Command daw-runner hosts the dashboard's code-defined agent runtime inside a
// Docker Sandbox. The browser UI and DAW control plane remain on the host.
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
	"strings"
	"syscall"
	"time"

	"github.com/rumpl/daw/internal/adapter/dagent"
	"github.com/rumpl/daw/internal/runnerapi"
)

const listenAddress = "0.0.0.0:8080"

var appVersion = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if strings.TrimSpace(os.Getenv("SANDBOX_VM_ID")) == "" {
		return errors.New("daw-runner must run inside a Docker Sandbox")
	}
	if strings.TrimSpace(os.Getenv("DAW_RUNNER_WORKSPACE")) == "" {
		return errors.New("DAW_RUNNER_WORKSPACE is required")
	}
	token := strings.TrimSpace(os.Getenv("DAW_RUNNER_TOKEN"))
	if token == "" {
		return errors.New("DAW_RUNNER_TOKEN is required")
	}

	logLevel := slog.LevelInfo
	if os.Getenv("DAWUI_DEBUG") != "" {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(slog.New(slog.DiscardHandler))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dataDir := "/home/agent/.cagent/daw-runner"
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create runner data directory: %w", err)
	}
	ad, err := dagent.New(ctx, dagent.Config{Logger: log, SessionDB: filepath.Join(dataDir, "session.db")})
	if err != nil {
		return fmt.Errorf("initialize sandbox docker-agent: %w", err)
	}
	runner := runnerapi.New(ad, token)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", listenAddress)
	if err != nil {
		runner.Shutdown(context.Background())
		return fmt.Errorf("listen on %s: %w", listenAddress, err)
	}
	defer listener.Close()

	httpServer := &http.Server{Handler: runner, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Info("sandbox runner started", "version", appVersion, "address", listenAddress)
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner.Shutdown(shutdownCtx)
	return httpServer.Shutdown(shutdownCtx)
}
