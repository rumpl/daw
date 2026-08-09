package plugins

import (
	"os"
	"path/filepath"
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
