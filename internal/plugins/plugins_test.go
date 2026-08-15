package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writePlugin(t *testing.T, base, id, manifest, entry string) {
	t.Helper()
	dir := filepath.Join(base, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogAndAssetFingerprint(t *testing.T) {
	base := t.TempDir()
	writePlugin(t, base, "hello", `{
		"apiVersion": 1,
		"id": "hello",
		"name": "Hello",
		"description": "A test plugin",
		"version": "1.0.0",
		"entry": "index.js",
		"pages": [{"id":"overview","path":"","label":"Hello","sidebar":true}]
	}`, `export function mount() {}`)

	catalog := Catalog(base)
	if len(catalog.Errors) != 0 || len(catalog.Plugins) != 1 {
		t.Fatalf("unexpected catalog: %#v", catalog)
	}
	plugin := catalog.Plugins[0]
	if plugin.EntryURL == "" || plugin.Fingerprint == "" || len(plugin.Pages) != 1 {
		t.Fatalf("incomplete plugin: %#v", plugin)
	}
	if plugin.Features == nil || !plugin.Features.Frontend || plugin.Features.Backend || plugin.Features.Styles || plugin.Features.Configuration {
		t.Fatalf("unexpected plugin features: %#v", plugin.Features)
	}
	path, _, err := Asset(base, "hello", plugin.Fingerprint, "index.js")
	if err != nil || filepath.Base(path) != "index.js" {
		t.Fatalf("resolve asset: %q %v", path, err)
	}

	if err := os.WriteFile(filepath.Join(base, "hello", "index.js"), []byte(`export function mount() { return () => {}; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	next := Catalog(base).Plugins[0]
	if next.Fingerprint == plugin.Fingerprint {
		t.Fatal("editing an asset must change the fingerprint")
	}
	if _, _, err := Asset(base, "hello", plugin.Fingerprint, "index.js"); err == nil {
		t.Fatal("stale fingerprint must not resolve")
	}
}

func TestCatalogAllowsPluginWithoutPages(t *testing.T) {
	base := t.TempDir()
	writePlugin(t, base, "background", `{
		"apiVersion":1,"id":"background","name":"Background","entry":"index.js"
	}`, `export function activate() {}`)

	catalog := Catalog(base)
	if len(catalog.Errors) != 0 || len(catalog.Plugins) != 1 || len(catalog.Plugins[0].Pages) != 0 {
		t.Fatalf("unexpected page-less plugin catalog: %#v", catalog)
	}
}

func TestCatalogAllowsBackendOnlyPlugin(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "backend-only")
	if err := os.MkdirAll(filepath.Join(dir, "backend"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"apiVersion":1,"id":"backend-only","name":"Backend only",
		"backend":{"entry":"backend/index.js"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backend", "index.js"), []byte(`export async function activate() {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog(base)
	if len(catalog.Errors) != 0 || len(catalog.Plugins) != 1 || catalog.Plugins[0].EntryURL != "" || catalog.Plugins[0].BackendURL == "" {
		t.Fatalf("unexpected backend-only catalog: %#v", catalog)
	}
}

func TestCatalogWithBackend(t *testing.T) {
	base := t.TempDir()
	writePlugin(t, base, "full-stack", `{
		"apiVersion":1,"id":"full-stack","name":"Full stack","entry":"index.js",
		"backend":{"entry":"backend/index.js"},
		"pages":[{"id":"main","path":"","label":"Full stack","sidebar":true}]
	}`, `export function mount() {}`)
	backendDir := filepath.Join(base, "full-stack", "backend")
	if err := os.MkdirAll(filepath.Join(backendDir, "node_modules", "example"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "index.js"), []byte(`export default () => new Response("ok")`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "node_modules", "example", "large.bin"), make([]byte, maxPluginFile+1), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := Catalog(base)
	if len(catalog.Errors) != 0 || len(catalog.Plugins) != 1 || catalog.Plugins[0].BackendURL == "" {
		t.Fatalf("unexpected backend catalog: %#v", catalog)
	}
	backend, err := ResolveBackend(base, "full-stack")
	if err != nil || backend.Entry != filepath.Join(backendDir, "index.js") {
		t.Fatalf("resolve backend: %+v, %v", backend, err)
	}
	if _, _, err := Asset(base, "full-stack", catalog.Plugins[0].Fingerprint, "backend/index.js"); err == nil {
		t.Fatal("backend files must not be browser assets")
	}
}

func TestMCPServersAreNamespacedAndWorkspaceResolved(t *testing.T) {
	base := t.TempDir()
	writePlugin(t, base, "mcp-plugin", `{
		"apiVersion":1,"id":"mcp-plugin","name":"MCP","entry":"index.js",
		"backend":{"entry":"backend/index.js","mcp":[
			{"id":"local","command":"node","args":["server.js"],"workingDir":"tools","env":{"TOKEN":"x"}},
			{"id":"remote","url":"https://example.test/mcp","transport":"streamable-http"}
		]}
	}`, `export function activate() {}`)
	if err := os.MkdirAll(filepath.Join(base, "mcp-plugin", "backend"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "mcp-plugin", "backend", "index.js"), []byte(`export default () => new Response()`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers := MCPServers(base)
	if len(servers) != 2 || servers[0].PluginID != "mcp-plugin" || servers[0].Name != "mcp-plugin-local" || servers[0].WorkingDir != "tools" ||
		len(servers[0].Env) != 1 || !slices.Contains(servers[0].Env, "TOKEN=x") || servers[1].Name != "mcp-plugin-remote" {
		t.Fatalf("unexpected MCP servers: %#v", servers)
	}
}

func TestCatalogReportsInvalidPluginsAndRejectsSymlinks(t *testing.T) {
	base := t.TempDir()
	writePlugin(t, base, "Bad_Name", `{}`, ``)
	writePlugin(t, base, "linked", `{
		"apiVersion":1,"id":"linked","name":"Linked","entry":"index.js",
		"pages":[{"id":"main","path":"","label":"Linked","sidebar":true}]
	}`, `export function mount() {}`)
	if err := os.Symlink(filepath.Join(base, "linked", "index.js"), filepath.Join(base, "linked", "other.js")); err != nil {
		t.Fatal(err)
	}

	catalog := Catalog(base)
	if len(catalog.Plugins) != 0 || len(catalog.Errors) != 2 {
		t.Fatalf("unexpected catalog: %#v", catalog)
	}
}

func TestContentTypes(t *testing.T) {
	if got := ContentType("entry.js"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("javascript content type: %q", got)
	}
	if got := ContentType("style.css"); got != "text/css; charset=utf-8" {
		t.Fatalf("stylesheet content type: %q", got)
	}
}
