package pluginoci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func testPlugin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "hello")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"apiVersion":1,"id":"hello","name":"Hello","version":"1.0.0","entry":"index.js"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(`export function activate() {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestImageIsDeterministicAndUsesPluginMediaTypes(t *testing.T) {
	dir := testPlugin(t)
	first, plugin, err := Image(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Image(dir)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := first.Digest()
	secondDigest, _ := second.Digest()
	if firstDigest != secondDigest {
		t.Fatalf("image digest changed: %s != %s", firstDigest, secondDigest)
	}
	if plugin.ID != "hello" {
		t.Fatalf("plugin id = %q", plugin.ID)
	}
	manifest, err := first.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Config.MediaType != ConfigMediaType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != LayerMediaType {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestExtractArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "link"} {
		t.Run(name, func(t *testing.T) {
			typeflag := byte(tar.TypeReg)
			if name == "link" {
				typeflag = tar.TypeSymlink
			}
			var compressed bytes.Buffer
			gzipWriter := gzip.NewWriter(&compressed)
			tarWriter := tar.NewWriter(gzipWriter)
			if err := tarWriter.WriteHeader(&tar.Header{Name: name, Typeflag: typeflag, Size: 0}); err != nil {
				t.Fatal(err)
			}
			if err := tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gzipWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := extractArchive(bytes.NewReader(compressed.Bytes()), t.TempDir()); err == nil {
				t.Fatal("expected unsafe archive to fail")
			}
		})
	}
}
