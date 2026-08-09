package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	mu            sync.Mutex
	dir           string
	dataDir       string
	apiOrigin     string
	csrf          string
	internalToken string
	active        func(string) bool
	log           *slog.Logger
	processes     map[string]*pluginBackendProcess
	stop          chan struct{}
	done          chan struct{}
}

type pluginBackendProcess struct {
	revision string
	cmd      *exec.Cmd
	proxy    *httputil.ReverseProxy
	done     chan struct{}
}

func newPluginBackendManager(dir, dataDir, apiOrigin, csrf string, active func(string) bool, log *slog.Logger) *pluginBackendManager {
	manager := &pluginBackendManager{dir: dir, dataDir: dataDir, apiOrigin: apiOrigin, csrf: csrf, internalToken: newToken(), active: active, log: log, processes: map[string]*pluginBackendProcess{}, stop: make(chan struct{}), done: make(chan struct{})}
	go manager.run()
	return manager
}

func (m *pluginBackendManager) run() {
	defer close(m.done)
	m.activateAll()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.activateAll()
		}
	}
}

func (m *pluginBackendManager) activateAll() {
	m.mu.Lock()
	apiOriginReady := m.apiOrigin != ""
	m.mu.Unlock()
	if !apiOriginReady {
		return
	}
	active := map[string]bool{}
	for _, plugin := range plugins.Catalog(m.dir).Plugins {
		if plugin.BackendURL == "" || (m.active != nil && !m.active(plugin.ID)) {
			continue
		}
		active[plugin.ID] = true
		backend, err := plugins.ResolveBackend(m.dir, plugin.ID)
		if err != nil {
			m.log.Warn("resolving plugin backend", "plugin", plugin.ID, "error", err)
			continue
		}
		if _, err := m.process(backend); err != nil {
			m.log.Warn("plugin backend activation failed", "plugin", plugin.ID, "error", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for pluginID, process := range m.processes {
		if active[pluginID] {
			continue
		}
		m.log.Info("stopping plugin backend", "plugin", pluginID, "reason", "plugin removed or backend disabled")
		_ = process.cmd.Process.Kill()
		<-process.done
		delete(m.processes, pluginID)
	}
}

func (m *pluginBackendManager) stopPlugin(pluginID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	process := m.processes[pluginID]
	if process == nil {
		return
	}
	m.log.Info("stopping plugin backend", "plugin", pluginID, "reason", reason)
	_ = process.cmd.Process.Kill()
	<-process.done
	delete(m.processes, pluginID)
}

func (m *pluginBackendManager) proxy(w http.ResponseWriter, r *http.Request, pluginID string) error {
	if m.active != nil && !m.active(pluginID) {
		return os.ErrNotExist
	}
	m.mu.Lock()
	if m.apiOrigin == "" {
		m.apiOrigin = "http://" + r.Host
	}
	m.mu.Unlock()
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

func (m *pluginBackendManager) proxyWebhook(w http.ResponseWriter, r *http.Request, pluginID, webhookID string) error {
	if m.active != nil && !m.active(pluginID) {
		return os.ErrNotExist
	}
	backend, err := plugins.ResolveBackend(m.dir, pluginID)
	if err != nil {
		return os.ErrNotExist
	}
	process, err := m.process(backend)
	if err != nil {
		return err
	}
	r.URL.Path = "/__daw/webhooks/" + webhookID
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
			m.log.Info("restarting plugin backend", "plugin", backend.PluginID, "reason", "backend changed")
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
	m.log.Info("starting plugin backend", "plugin", backend.PluginID, "revision", backend.Revision)
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
		"DAW_PLUGIN_TOKEN="+m.internalToken,
		"DAW_PLUGIN_ID="+backend.PluginID,
		"DAW_PLUGIN_DATA_DIR="+filepath.Join(m.dataDir, backend.PluginID),
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
			m.log.Warn("plugin backend stderr", "plugin", backend.PluginID, "error", errors.New(strings.TrimSpace(string(data))))
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
			m.log.Warn("plugin backend proxy", "plugin", backend.PluginID, "error", proxyErr)
			http.Error(w, `{"error":"plugin backend unavailable","code":"plugin_backend_unavailable"}`, http.StatusBadGateway)
		}
		done := make(chan struct{})
		go func() {
			err := cmd.Wait()
			close(done)
			m.log.Info("plugin backend exited", "plugin", backend.PluginID, "pid", cmd.Process.Pid, "error", err)
		}()
		m.log.Info("plugin backend activated", "plugin", backend.PluginID, "revision", backend.Revision, "pid", cmd.Process.Pid, "port", result.message.Port)
		return &pluginBackendProcess{revision: backend.Revision, cmd: cmd, proxy: proxy, done: done}, nil
	case <-time.After(backendStartupTimeout):
		_ = cmd.Process.Kill()
		return nil, errors.New("plugin backend startup timed out")
	}
}

func (m *pluginBackendManager) close(ctx context.Context) {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, process := range m.processes {
		m.log.Info("stopping plugin backend", "plugin", id, "reason", "server shutdown")
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
import fs from "node:fs/promises";
import path from "node:path";
export class DashboardApiError extends Error {
  constructor(message, code, status, details) { super(message); this.name = "DashboardApiError"; this.code = code; this.status = status; this.details = details; }
}
export function createDashboardClient() {
  const origin = process.env.DAW_API_ORIGIN;
  const token = process.env.DAW_API_TOKEN;
  if (!origin || !token) throw new Error("dashboard backend environment is unavailable");
  return {
    async raw(method, requestPath, body, options = {}) {
      if (typeof requestPath !== "string" || !requestPath.startsWith("/api/")) throw new TypeError("dashboard API paths must start with /api/");
      const headers = new Headers(options.headers);
      if (!["GET", "HEAD", "OPTIONS"].includes(method.toUpperCase())) headers.set("X-DAW-CSRF", token);
      headers.set("X-DAW-Plugin-ID", pluginId);
      headers.set("X-DAW-Plugin-Token", process.env.DAW_PLUGIN_TOKEN);
      let payload = body;
      if (body !== undefined && !(body instanceof FormData) && typeof body !== "string" && !ArrayBuffer.isView(body)) {
        headers.set("Content-Type", "application/json"); payload = JSON.stringify(body);
      }
      return fetch(new URL(requestPath, origin), { ...options, method, headers, body: payload, redirect: "error" });
    },
    async request(method, requestPath, body, options) {
      const response = await this.raw(method, requestPath, body, options);
      const value = response.status === 204 ? undefined : await response.json();
      if (!response.ok) throw new DashboardApiError(value?.error || "dashboard API request failed", value?.code || "api_error", response.status, value?.details);
      return value;
    }
  };
}
export const dashboard = createDashboardClient();
export const pluginId = process.env.DAW_PLUGIN_ID;
export async function registerExecutionLocation(workspaceId, workingDir, options = {}) {
  return dashboard.request("POST", "/api/plugins/" + pluginId + "/execution-locations", {
    workspaceId, workingDir, ...(options.ttlSeconds ? {ttlSeconds: options.ttlSeconds} : {})
  });
}
const dataDir = process.env.DAW_PLUGIN_DATA_DIR;
function storagePath(key) {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(key)) throw new TypeError("storage keys must be simple names");
  return path.join(dataDir, "storage", key + ".json");
}
export const storage = Object.freeze({
  async get(key) { try { return JSON.parse(await fs.readFile(storagePath(key), "utf8")); } catch (error) { if (error?.code === "ENOENT") return undefined; throw error; } },
  async set(key, value) { const file = storagePath(key); await fs.mkdir(path.dirname(file), {recursive:true, mode:0o700}); const data = JSON.stringify(value); if (Buffer.byteLength(data) > 262144) throw new RangeError("storage value exceeds 256 KiB"); await fs.writeFile(file + ".tmp", data, {mode:0o600}); await fs.rename(file + ".tmp", file); },
  async delete(key) { try { await fs.unlink(storagePath(key)); } catch (error) { if (error?.code !== "ENOENT") throw error; } }
});
export const configuration = Object.freeze({
  async get() { return (await dashboard.request("GET", "/api/plugins/" + pluginId + "/config")).values; },
  async set(values) { return (await dashboard.request("PUT", "/api/plugins/" + pluginId + "/config", {values})).values; }
});
export const webhooks = Object.freeze({
  async credentials(id) { return dashboard.request("GET", "/api/plugins/" + pluginId + "/webhooks/" + encodeURIComponent(id) + "/token"); }
});
export const events = Object.freeze({
  async publish(type, data) { return dashboard.request("POST", "/api/plugins/" + pluginId + "/publish", {type, data}); },
  subscribeDashboard(listener, options = {}) {
    const controller = new AbortController();
    let last = 0;
    (async () => {
      while (!controller.signal.aborted) {
        try {
          const suffix = last ? "?lastEventId=" + last : "";
          const response = await dashboard.raw("GET", "/api/events" + suffix, undefined, {signal:controller.signal});
          const reader = response.body.pipeThrough(new TextDecoderStream()).getReader(); let buffer = "";
          while (true) { const {done,value} = await reader.read(); if (done) break; buffer += value; let cut; while ((cut = buffer.indexOf("\n\n")) >= 0) { const frame = buffer.slice(0, cut); buffer = buffer.slice(cut + 2); const line = frame.split("\n").find(v => v.startsWith("data: ")); if (!line) continue; const event = JSON.parse(line.slice(6)); if (event.seq) last = event.seq; if (!options.types || options.types.includes(event.type)) await listener(event); } }
        } catch (error) { if (controller.signal.aborted) break; await new Promise(resolve => setTimeout(resolve, 1000)); }
      }
    })();
    return () => controller.abort();
  }
});
`

const backendRunnerSource = `
import http from "node:http";
import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";
const module = await import(pathToFileURL(process.argv[2]));
const handler = module.default || module.handler;
const webhook = module.webhook;
const activate = module.activate;
if (handler !== undefined && typeof handler !== "function") throw new Error("backend handler must be a function");
if (webhook !== undefined && typeof webhook !== "function") throw new Error("backend webhook must be a function");
let deactivate;
if (typeof activate === "function") deactivate = await activate({pluginId:process.env.DAW_PLUGIN_ID});
const server = http.createServer(async (incoming, outgoing) => {
  try {
    const controller = new AbortController();
    incoming.on("aborted", () => controller.abort());
    outgoing.on("close", () => { if (!outgoing.writableEnded) controller.abort(); });
    const requestBody = ["GET", "HEAD"].includes(incoming.method) ? undefined : Readable.toWeb(incoming);
    const request = new Request("http://plugin.local" + incoming.url, {method:incoming.method, headers:incoming.headers, body:requestBody, signal:controller.signal, ...(requestBody ? {duplex:"half"} : {})});
    const webhookPrefix = "/__daw/webhooks/";
    const selected = incoming.url.startsWith(webhookPrefix) ? webhook : handler;
    if (typeof selected !== "function") { outgoing.writeHead(404, {"content-type":"application/json"}); outgoing.end(JSON.stringify({error:"not found",code:"not_found"})); return; }
    const response = await selected(request, {pluginId:process.env.DAW_PLUGIN_ID, webhookId:incoming.url.startsWith(webhookPrefix) ? incoming.url.slice(webhookPrefix.length).split("?")[0] : undefined});
    if (!(response instanceof Response)) throw new Error("backend handler must return a Response");
    outgoing.writeHead(response.status, Object.fromEntries(response.headers));
    if (!response.body) { outgoing.end(); return; }
    Readable.fromWeb(response.body).on("error", error => outgoing.destroy(error)).pipe(outgoing);
  } catch (error) {
    if (outgoing.headersSent) { outgoing.destroy(error); return; }
    console.error(error?.stack || error);
    outgoing.writeHead(500, {"content-type":"application/json"});
    outgoing.end(JSON.stringify({error:"plugin backend failed",code:"plugin_backend_error"}));
  }
});
server.listen(0, "127.0.0.1", () => console.log(JSON.stringify({port:server.address().port})));
async function shutdown() { try { if (typeof deactivate === "function") await deactivate(); } finally { server.close(() => process.exit(0)); } }
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, shutdown);
`
