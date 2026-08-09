package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rumpl/daw/internal/plugins"
)

const backendStartupTimeout = 10 * time.Second

type pluginBackendManager struct {
	mu        sync.Mutex
	dir       string
	apiOrigin string
	csrf      string
	logError  func(string, error)
	processes map[string]*pluginBackendProcess
}

type pluginBackendProcess struct {
	revision string
	cmd      *exec.Cmd
	proxy    *httputil.ReverseProxy
	done     chan struct{}
}

func newPluginBackendManager(dir, apiOrigin, csrf string, logError func(string, error)) *pluginBackendManager {
	return &pluginBackendManager{dir: dir, apiOrigin: apiOrigin, csrf: csrf, logError: logError, processes: map[string]*pluginBackendProcess{}}
}

func (m *pluginBackendManager) proxy(w http.ResponseWriter, r *http.Request, pluginID string) error {
	if m.apiOrigin == "" {
		m.mu.Lock()
		if m.apiOrigin == "" {
			m.apiOrigin = "http://" + r.Host
		}
		m.mu.Unlock()
	}
	backend, err := plugins.ResolveBackend(m.dir, pluginID)
	if err != nil {
		return os.ErrNotExist
	}
	process, err := m.process(backend)
	if err != nil {
		return err
	}
	prefix := "/api/plugins/" + pluginID + "/backend"
	r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	process.proxy.ServeHTTP(w, r)
	return nil
}

func (m *pluginBackendManager) process(backend plugins.Backend) (*pluginBackendProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.processes[backend.PluginID]; current != nil {
		select {
		case <-current.done:
			delete(m.processes, backend.PluginID)
		default:
			if current.revision == backend.Revision {
				return current, nil
			}
			_ = current.cmd.Process.Kill()
			<-current.done
			delete(m.processes, backend.PluginID)
		}
	}
	process, err := m.start(backend)
	if err != nil {
		return nil, err
	}
	m.processes[backend.PluginID] = process
	return process, nil
}

func (m *pluginBackendManager) start(backend plugins.Backend) (*pluginBackendProcess, error) {
	if err := installBackendSDK(backend.Directory); err != nil {
		return nil, fmt.Errorf("installing backend SDK: %w", err)
	}
	runner, err := writeBackendRunner()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.Background(), "node", runner, backend.Entry)
	cmd.Dir = backend.Directory
	cmd.Env = append(os.Environ(),
		"DAW_API_ORIGIN="+m.apiOrigin,
		"DAW_API_TOKEN="+m.csrf,
		"DAW_PLUGIN_ID="+backend.PluginID,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting node: %w", err)
	}
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stderr, 1<<20))
		if len(data) > 0 {
			m.logError(backend.PluginID, errors.New(strings.TrimSpace(string(data))))
		}
	}()

	type readyMessage struct {
		Port int `json:"port"`
	}
	ready := make(chan struct {
		message readyMessage
		err     error
	}, 1)
	reader := bufio.NewReader(stdout)
	go func() {
		line, readErr := reader.ReadBytes('\n')
		var message readyMessage
		if readErr == nil {
			readErr = json.Unmarshal(line, &message)
		}
		ready <- struct {
			message readyMessage
			err     error
		}{message, readErr}
		if readErr == nil {
			_, _ = io.Copy(io.Discard, reader)
		}
	}()
	select {
	case result := <-ready:
		if result.err != nil || result.message.Port < 1 {
			_ = cmd.Process.Kill()
			return nil, errors.New("plugin backend did not start correctly")
		}
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", result.message.Port))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
			m.logError(backend.PluginID, proxyErr)
			http.Error(w, `{"error":"plugin backend unavailable","code":"plugin_backend_unavailable"}`, http.StatusBadGateway)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		return &pluginBackendProcess{revision: backend.Revision, cmd: cmd, proxy: proxy, done: done}, nil
	case <-time.After(backendStartupTimeout):
		_ = cmd.Process.Kill()
		return nil, errors.New("plugin backend startup timed out")
	}
}

func (m *pluginBackendManager) close(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, process := range m.processes {
		_ = process.cmd.Process.Signal(os.Interrupt)
		select {
		case <-process.done:
		case <-ctx.Done():
			_ = process.cmd.Process.Kill()
		}
		delete(m.processes, id)
	}
}

func writeBackendRunner() (string, error) {
	dir := filepath.Join(os.TempDir(), "dawui-plugin-runtime")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "runner.mjs")
	if err := os.WriteFile(path, []byte(backendRunnerSource), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func installBackendSDK(dir string) error {
	packageDir := filepath.Join(dir, "node_modules", "@daw", "plugin-backend")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		return err
	}
	pkg := `{"name":"@daw/plugin-backend","version":"1.0.0","type":"module","exports":"./index.js"}`
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(pkg), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(packageDir, "index.js"), []byte(backendSDKSource), 0o600)
}

const backendSDKSource = `
export class DashboardApiError extends Error {
  constructor(message, code, status, details) { super(message); this.name = "DashboardApiError"; this.code = code; this.status = status; this.details = details; }
}
export function createDashboardClient() {
  const origin = process.env.DAW_API_ORIGIN;
  const token = process.env.DAW_API_TOKEN;
  if (!origin || !token) throw new Error("dashboard backend environment is unavailable");
  return {
    async raw(method, path, body, options = {}) {
      if (typeof path !== "string" || !path.startsWith("/api/")) throw new TypeError("dashboard API paths must start with /api/");
      const headers = new Headers(options.headers);
      if (!["GET", "HEAD", "OPTIONS"].includes(method.toUpperCase())) headers.set("X-DAW-CSRF", token);
      let payload = body;
      if (body !== undefined && !(body instanceof FormData) && typeof body !== "string" && !ArrayBuffer.isView(body)) {
        headers.set("Content-Type", "application/json"); payload = JSON.stringify(body);
      }
      return fetch(new URL(path, origin), { ...options, method, headers, body: payload, redirect: "error" });
    },
    async request(method, path, body, options) {
      const response = await this.raw(method, path, body, options);
      const value = response.status === 204 ? undefined : await response.json();
      if (!response.ok) throw new DashboardApiError(value?.error || "dashboard API request failed", value?.code || "api_error", response.status, value?.details);
      return value;
    }
  };
}
export const dashboard = createDashboardClient();
export const pluginId = process.env.DAW_PLUGIN_ID;
`

const backendRunnerSource = `
import http from "node:http";
import { pathToFileURL } from "node:url";
const module = await import(pathToFileURL(process.argv[2]));
const handler = module.default || module.handler;
if (typeof handler !== "function") throw new Error("backend entry must export default or handler");
const server = http.createServer(async (incoming, outgoing) => {
  try {
    const chunks = []; for await (const chunk of incoming) chunks.push(chunk);
    const body = ["GET", "HEAD"].includes(incoming.method) ? undefined : Buffer.concat(chunks);
    const request = new Request("http://plugin.local" + incoming.url, { method: incoming.method, headers: incoming.headers, body, ...(body ? { duplex: "half" } : {}) });
    const response = await handler(request, { pluginId: process.env.DAW_PLUGIN_ID });
    if (!(response instanceof Response)) throw new Error("backend handler must return a Response");
    outgoing.writeHead(response.status, Object.fromEntries(response.headers));
    outgoing.end(Buffer.from(await response.arrayBuffer()));
  } catch (error) {
    console.error(error?.stack || error);
    outgoing.writeHead(500, { "content-type": "application/json" });
    outgoing.end(JSON.stringify({ error: "plugin backend failed", code: "plugin_backend_error" }));
  }
});
server.listen(0, "127.0.0.1", () => console.log(JSON.stringify({ port: server.address().port })));
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
`
