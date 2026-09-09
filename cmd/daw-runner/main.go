// Command daw-runner hosts the dashboard's code-defined agent runtime inside a
// Docker Sandbox. Control, events, store RPC, and host callbacks are carried
// over a multiplexed sbx-exec stdin/stdout connection; stdout is protocol-only.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rumpl/daw/internal/adapter/dagent"
	"github.com/rumpl/daw/internal/mcpbridge"
	"github.com/rumpl/daw/internal/runnerapi"
	"github.com/rumpl/daw/internal/sessionstoreremote"
	"github.com/rumpl/daw/internal/stdiomux"
)

const (
	callbackAddress = "127.0.0.1:8081"
	mcpRelayAddress = "127.0.0.1:8082"
)

var appVersion = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 3 && os.Args[1] == "mcp-relay" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return mcpbridge.Relay(ctx, os.Args[2], os.Stdin, os.Stdout)
	}
	if strings.TrimSpace(os.Getenv("DAW_RUNNER_WORKSPACE")) == "" {
		return errors.New("DAW_RUNNER_WORKSPACE is required")
	}
	runnerToken := strings.TrimSpace(os.Getenv("DAW_RUNNER_TOKEN"))
	if runnerToken == "" {
		return errors.New("DAW_RUNNER_TOKEN is required")
	}
	storeToken := strings.TrimSpace(os.Getenv("DAW_SESSION_STORE_TOKEN"))
	if storeToken == "" {
		return errors.New("DAW_SESSION_STORE_TOKEN is required")
	}

	level := slog.LevelInfo
	if os.Getenv("DAWUI_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(slog.New(slog.DiscardHandler))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	peer, err := stdiomux.New(os.Stdin, os.Stdout, stdiomux.Runner)
	if err != nil {
		return err
	}
	defer peer.Close()

	store, err := sessionstoreremote.New(sessionstoreremote.Config{URL: "http://session-store", Token: storeToken, DialContext: peer.DialContext})
	if err != nil {
		return fmt.Errorf("configure host session store: %w", err)
	}
	if err := store.Check(ctx); err != nil {
		_ = store.Close()
		return err
	}
	ad, err := dagent.New(ctx, dagent.Config{Logger: log, SessionStore: store, OwnStore: true, StoreLabel: "stdio://host/session-store"})
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("initialize sandbox docker-agent: %w", err)
	}
	runner := runnerapi.New(ad, runnerToken)

	// Plugin backends keep their HTTP API contract, but their loopback request
	// is forwarded to the host over a runner-initiated mux stream.
	target, _ := url.Parse("http://mcp-callback")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = peer.DialContext
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Rewrite = func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = target.Host
	}
	proxy.Transport = transport
	callbackListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", callbackAddress)
	if err != nil {
		runner.Shutdown(context.Background())
		return fmt.Errorf("listen for sandbox plugin callbacks: %w", err)
	}
	callbackServer := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = callbackServer.Serve(callbackListener) }()

	// Local command MCP servers always run on the host. The process spawned by
	// docker-agent in this sandbox connects here and is forwarded byte-for-byte
	// to the host launch broker over the existing reverse stdio mux.
	mcpListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", mcpRelayAddress)
	if err != nil {
		_ = callbackServer.Shutdown(context.Background())
		runner.Shutdown(context.Background())
		return fmt.Errorf("listen for sandbox MCP relay: %w", err)
	}
	go forwardMCPRelays(ctx, mcpListener, peer)

	httpServer := &http.Server{Handler: runner, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Info("sandbox runner started", "version", appVersion, "transport", "stdio")
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(peer) }()
	select {
	case <-ctx.Done():
	case <-peer.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner.Shutdown(shutdownCtx)
	_ = callbackServer.Shutdown(shutdownCtx)
	_ = mcpListener.Close()
	transport.CloseIdleConnections()
	return httpServer.Shutdown(shutdownCtx)
}

func forwardMCPRelays(ctx context.Context, listener net.Listener, peer *stdiomux.Mux) {
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer local.Close()
			host, err := peer.DialContext(ctx, "tcp", "mcp-command")
			if err != nil {
				return
			}
			defer host.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(host, local); done <- struct{}{} }()
			go func() { _, _ = io.Copy(local, host); done <- struct{}{} }()
			<-done
		}()
	}
}
