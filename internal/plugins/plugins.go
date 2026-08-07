// Package plugins discovers and serves trusted, global dashboard plugins.
package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/rumpl/daw/internal/protocol"
)

const (
	manifestName   = "plugin.json"
	maxManifest    = 64 << 10
	maxPluginFile  = 4 << 20
	maxPluginSize  = 16 << 20
	maxPluginFiles = 200
)

var (
	idPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	pagePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	pathPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/-]*$`)
)

type manifest struct {
	APIVersion  int                   `json:"apiVersion"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Version     string                `json:"version"`
	Entry       string                `json:"entry"`
	Style       string                `json:"style"`
	Pages       []protocol.PluginPage `json:"pages"`
}

// Catalog scans dir and returns every valid plugin plus bounded diagnostics for
// invalid plugin directories. A missing directory is an empty catalog.
func Catalog(dir string) protocol.PluginCatalog {
	out := protocol.PluginCatalog{Plugins: []protocol.Plugin{}, Errors: []protocol.PluginError{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.Errors = append(out.Errors, protocol.PluginError{Message: "the plugin directory could not be read"})
		}
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		plugin, err := load(dir, entry.Name())
		if err != nil {
			out.Errors = append(out.Errors, protocol.PluginError{
				PluginID: entry.Name(),
				Message:  err.Error(),
			})
			continue
		}
		out.Plugins = append(out.Plugins, plugin)
	}
	sort.Slice(out.Plugins, func(i, j int) bool {
		left, right := strings.ToLower(out.Plugins[i].Name), strings.ToLower(out.Plugins[j].Name)
		if left == right {
			return out.Plugins[i].ID < out.Plugins[j].ID
		}
		return left < right
	})
	sort.Slice(out.Errors, func(i, j int) bool { return out.Errors[i].PluginID < out.Errors[j].PluginID })
	return out
}

func load(base, directory string) (protocol.Plugin, error) {
	if !idPattern.MatchString(directory) {
		return protocol.Plugin{}, errors.New("directory name must be a lowercase kebab-case plugin id")
	}
	root := filepath.Join(base, directory)
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		return protocol.Plugin{}, errors.New("plugin.json is missing or unreadable")
	}
	if len(data) > maxManifest {
		return protocol.Plugin{}, errors.New("plugin.json is too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var m manifest
	if err := dec.Decode(&m); err != nil {
		return protocol.Plugin{}, errors.New("plugin.json is not valid")
	}
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return protocol.Plugin{}, errors.New("plugin.json must contain one object")
	}
	if m.APIVersion != 1 {
		return protocol.Plugin{}, errors.New("apiVersion must be 1")
	}
	if m.ID != directory {
		return protocol.Plugin{}, errors.New("manifest id must match its directory name")
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 80 {
		return protocol.Plugin{}, errors.New("name is required and must be at most 80 characters")
	}
	if len(m.Description) > 300 || len(m.Version) > 40 {
		return protocol.Plugin{}, errors.New("description or version is too long")
	}
	if err := validAssetPath(m.Entry, ".js", ".mjs"); err != nil {
		return protocol.Plugin{}, fmt.Errorf("entry %w", err)
	}
	if m.Style != "" {
		if err := validAssetPath(m.Style, ".css"); err != nil {
			return protocol.Plugin{}, fmt.Errorf("style %w", err)
		}
	}
	if len(m.Pages) == 0 || len(m.Pages) > 30 {
		return protocol.Plugin{}, errors.New("pages must contain between 1 and 30 entries")
	}
	pageIDs, pagePaths := map[string]bool{}, map[string]bool{}
	for i := range m.Pages {
		page := &m.Pages[i]
		if !pagePattern.MatchString(page.ID) {
			return protocol.Plugin{}, errors.New("page ids must be lowercase kebab-case")
		}
		page.Path = strings.Trim(page.Path, "/")
		if page.Path != "" && (!pathPattern.MatchString(page.Path) || strings.Contains(page.Path, "//")) {
			return protocol.Plugin{}, errors.New("page paths must contain lowercase URL path components")
		}
		if strings.TrimSpace(page.Label) == "" || len(page.Label) > 60 {
			return protocol.Plugin{}, errors.New("page labels are required and must be at most 60 characters")
		}
		if pageIDs[page.ID] || pagePaths[page.Path] {
			return protocol.Plugin{}, errors.New("page ids and paths must be unique")
		}
		pageIDs[page.ID], pagePaths[page.Path] = true, true
	}
	fingerprint, err := fingerprint(root)
	if err != nil {
		return protocol.Plugin{}, err
	}
	if !regularFile(filepath.Join(root, filepath.FromSlash(m.Entry))) {
		return protocol.Plugin{}, errors.New("entry file is missing or is not a regular file")
	}
	if m.Style != "" && !regularFile(filepath.Join(root, filepath.FromSlash(m.Style))) {
		return protocol.Plugin{}, errors.New("style file is missing or is not a regular file")
	}
	prefix := "/api/plugins/" + m.ID + "/assets/" + fingerprint + "/"
	plugin := protocol.Plugin{
		APIVersion: m.APIVersion, ID: m.ID, Name: m.Name,
		Description: m.Description, Version: m.Version, Fingerprint: fingerprint,
		EntryURL: prefix + escapeAssetPath(m.Entry), Pages: m.Pages,
	}
	if m.Style != "" {
		plugin.StyleURL = prefix + escapeAssetPath(m.Style)
	}
	return plugin, nil
}

func escapeAssetPath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func validAssetPath(value string, extensions ...string) error {
	if value == "" || !fs.ValidPath(value) || strings.HasPrefix(value, ".") {
		return errors.New("must be a relative path inside the plugin")
	}
	ext := strings.ToLower(filepath.Ext(value))
	if slices.Contains(extensions, ext) {
		return nil
	}
	return fmt.Errorf("must use one of these extensions: %s", strings.Join(extensions, ", "))
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func fingerprint(root string) (string, error) {
	type file struct {
		path string
		size int64
	}
	var files []file
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("plugin files could not be read")
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("plugin symlinks are not supported")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("plugins may contain only directories and regular files")
		}
		if info.Size() > maxPluginFile {
			return errors.New("a plugin file exceeds the 4 MiB limit")
		}
		total += info.Size()
		if total > maxPluginSize {
			return errors.New("plugin exceeds the 16 MiB limit")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return errors.New("plugin files could not be indexed")
		}
		files = append(files, file{path: filepath.ToSlash(rel), size: info.Size()})
		if len(files) > maxPluginFiles {
			return errors.New("plugin contains more than 200 files")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	hash := sha256.New()
	for _, item := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.path)))
		if err != nil {
			return "", errors.New("plugin files could not be read")
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", item.path, item.size)
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
}

// Asset resolves a current plugin asset. The fingerprint must match the
// current catalog, making asset URLs immutable while invalidating stale code
// immediately after an edit.
func Asset(dir, pluginID, expectedFingerprint, assetPath string) (string, os.FileInfo, error) {
	plugin, err := load(dir, pluginID)
	if err != nil || plugin.Fingerprint != expectedFingerprint {
		return "", nil, os.ErrNotExist
	}
	if !fs.ValidPath(assetPath) || strings.HasPrefix(assetPath, ".") {
		return "", nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(filepath.Join(dir, pluginID))
	if err != nil {
		return "", nil, os.ErrNotExist
	}
	defer root.Close()
	file, err := root.Open(assetPath)
	if err != nil {
		return "", nil, os.ErrNotExist
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPluginFile {
		return "", nil, os.ErrNotExist
	}
	path := filepath.Join(dir, pluginID, filepath.FromSlash(assetPath))
	return path, info, nil
}

// ContentType returns explicit module and stylesheet MIME types; other assets
// use the platform MIME registry and fall back to binary data.
func ContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	default:
		if value := mime.TypeByExtension(filepath.Ext(path)); value != "" {
			return value
		}
		return "application/octet-stream"
	}
}
