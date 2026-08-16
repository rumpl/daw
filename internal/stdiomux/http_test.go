package stdiomux

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPInBothDirections(t *testing.T) {
	host, runner := pair(t)
	hostServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "host:"+r.URL.Path) })}
	runnerServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "runner:"+r.URL.Path) })}
	go func() { _ = hostServer.Serve(host) }()
	go func() { _ = runnerServer.Serve(runner) }()
	t.Cleanup(func() { _ = hostServer.Close(); _ = runnerServer.Close() })

	request := func(dial func(ctx context.Context, network, address string) (net.Conn, error), target, want string) {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = dial
		defer transport.CloseIdleConnections()
		res, err := (&http.Client{Transport: transport}).Get(target)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(data)) != want {
			t.Fatalf("got %q want %q", data, want)
		}
	}
	request(host.DialContext, "http://runner/ping", "runner:/ping")
	request(runner.DialContext, "http://host/store", "host:/store")
}
