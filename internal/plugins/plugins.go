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

	"github.com/rumpl/daw/internal/adapter"
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
	Backend     *backendManifest      `json:"backend"`
	Config      map[string]any        `json:"configuration"`
	Pages       []protocol.PluginPage `json:"pages"`
}

type backendManifest struct {
	Entry    string            `json:"entry"`
	Webhooks []webhookManifest `json:"webhooks"`
	MCP      []mcpManifest     `json:"mcp"`
}

type mcpManifest struct {
	ID         string            `json:"id"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	WorkingDir string            `json:"workingDir"`
	URL        string            `json:"url"`
	Transport  string            `json:"transport"`
	Headers    map[string]string `json:"headers"`
}

type webhookManifest struct {
	ID string `json:"id"`
}

type Backend struct {
	PluginID  string
	Directory string
	Entry     string
	Revision  string
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
			out.Errors = append(out.Errors, protocol.PluginError{PluginID: entry.Name(), Message: err.Error()})
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
	if strings.TrimSpace(m.Entry) == "" && m.Backend == nil {
		return protocol.Plugin{}, errors.New("entry or backend is required")
	}
	if m.Entry == "" && len(m.Pages) > 0 {
		return protocol.Plugin{}, errors.New("pages require a frontend entry")
	}
	if m.Entry != "" {
		if err := validAssetPath(m.Entry, ".js", ".mjs"); err != nil {
			return protocol.Plugin{}, fmt.Errorf("entry %w", err)
		}
	}
	if m.Style != "" {
		if err := validAssetPath(m.Style, ".css"); err != nil {
			return protocol.Plugin{}, fmt.Errorf("style %w", err)
		}
	}
	if m.Backend != nil {
		if err := validBackendEntry(m.Backend.Entry); err != nil {
			return protocol.Plugin{}, fmt.Errorf("backend entry %w", err)
		}
		if !regularFile(filepath.Join(root, filepath.FromSlash(m.Backend.Entry))) {
			return protocol.Plugin{}, errors.New("backend entry file is missing or is not a regular file")
		}
		if len(m.Backend.Webhooks) > 20 {
			return protocol.Plugin{}, errors.New("backend may declare at most 20 webhooks")
		}
		seenWebhooks := map[string]bool{}
		for _, webhook := range m.Backend.Webhooks {
			if !idPattern.MatchString(webhook.ID) || seenWebhooks[webhook.ID] {
				return protocol.Plugin{}, errors.New("webhook ids must be unique lowercase kebab-case values")
			}
			seenWebhooks[webhook.ID] = true
		}
		if len(m.Backend.MCP) > 20 {
			return protocol.Plugin{}, errors.New("backend may declare at most 20 MCP servers")
		}
		seenMCP := map[string]bool{}
		for _, server := range m.Backend.MCP {
			if !idPattern.MatchString(server.ID) || seenMCP[server.ID] || (server.Command == "") == (server.URL == "") {
				return protocol.Plugin{}, errors.New("MCP servers require a unique id and exactly one command or url")
			}
			if server.URL != "" {
				parsed, err := url.Parse(server.URL)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
					return protocol.Plugin{}, errors.New("remote MCP server urls must use http or https")
				}
				if server.Transport != "" && server.Transport != "sse" && server.Transport != "streamable" && server.Transport != "streamable-http" {
					return protocol.Plugin{}, errors.New("remote MCP transport is invalid")
				}
			}
			if server.Command != "" && strings.Contains(server.Command, "/") &&
				(!fs.ValidPath(server.Command) || strings.HasPrefix(server.Command, ".")) {
				return protocol.Plugin{}, errors.New("MCP command paths must be relative paths inside the backend")
			}
			if len(server.Args) > 100 || len(server.Env) > 100 || len(server.Headers) > 100 {
				return protocol.Plugin{}, errors.New("MCP args, env, or headers contain too many entries")
			}
			for key, value := range server.Env {
				if key == "" || strings.ContainsAny(key, "=\x00") || len(key) > 128 || len(value) > 4096 {
					return protocol.Plugin{}, errors.New("MCP environment contains an invalid entry")
				}
			}
			for key, value := range server.Headers {
				if strings.TrimSpace(key) == "" || strings.ContainsAny(key+value, "\r\n") || len(key) > 128 || len(value) > 4096 {
					return protocol.Plugin{}, errors.New("MCP headers contain an invalid entry")
				}
			}
			if strings.Contains(server.WorkingDir, "..") || filepath.IsAbs(server.WorkingDir) {
				return protocol.Plugin{}, errors.New("MCP workingDir must be relative to the workspace")
			}
			seenMCP[server.ID] = true
		}
	}
	if m.Config != nil {
		data, err := json.Marshal(m.Config)
		if err != nil || len(data) > maxManifest {
			return protocol.Plugin{}, errors.New("configuration schema is too large or invalid")
		}
	}
	if len(m.Pages) > 30 {
		return protocol.Plugin{}, errors.New("pages may contain at most 30 entries")
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
	fingerprint, err := fingerprint(root, backendDirectory(m.Backend))
	if err != nil {
		return protocol.Plugin{}, err
	}
	if m.Entry != "" && !regularFile(filepath.Join(root, filepath.FromSlash(m.Entry))) {
		return protocol.Plugin{}, errors.New("entry file is missing or is not a regular file")
	}
	if m.Style != "" && !regularFile(filepath.Join(root, filepath.FromSlash(m.Style))) {
		return protocol.Plugin{}, errors.New("style file is missing or is not a regular file")
	}
	prefix := "/api/plugins/" + m.ID + "/assets/" + fingerprint + "/"
	features := protocol.PluginFeatures{
		Frontend: m.Entry != "", Styles: m.Style != "", Backend: m.Backend != nil,
		Configuration: m.Config != nil, Webhooks: []string{}, MCPServers: []protocol.PluginMCPServer{},
	}
	if m.Backend != nil {
		for _, webhook := range m.Backend.Webhooks {
			features.Webhooks = append(features.Webhooks, webhook.ID)
		}
		for _, server := range m.Backend.MCP {
			transport := "stdio"
			if server.URL != "" {
				transport = server.Transport
				if transport == "" {
					transport = "remote"
				}
			}
			features.MCPServers = append(features.MCPServers, protocol.PluginMCPServer{ID: server.ID, Transport: transport})
		}
	}
	plugin := protocol.Plugin{
		APIVersion: m.APIVersion, ID: m.ID, Name: m.Name,
		Description: m.Description, Version: m.Version, Fingerprint: fingerprint,
		Pages: m.Pages, Configuration: m.Config, Features: &features,
	}
	if m.Entry != "" {
		plugin.EntryURL = prefix + escapeAssetPath(m.Entry)
	}
	if m.Backend != nil {
		plugin.BackendURL = "/api/plugins/" + m.ID + "/backend"
		plugin.EventsURL = "/api/plugins/" + m.ID + "/events"
		plugin.ConfigURL = "/api/plugins/" + m.ID + "/config"
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

func validBackendEntry(value string) error {
	if err := validAssetPath(value, ".js", ".mjs", ".cjs"); err != nil {
		return err
	}
	if !strings.Contains(value, "/") {
		return errors.New("must be inside a backend directory")
	}
	return nil
}

func backendDirectory(backend *backendManifest) string {
	if backend == nil {
		return ""
	}
	return strings.Split(backend.Entry, "/")[0]
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func fingerprint(root, excludedDirectory string) (string, error) {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return errors.New("plugin files could not be indexed")
		}
		relSlash := filepath.ToSlash(rel)
		if excludedDirectory != "" && (relSlash == excludedDirectory || strings.HasPrefix(relSlash, excludedDirectory+"/")) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
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
		files = append(files, file{path: relSlash, size: info.Size()})
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
	backendDir := ""
	data, readErr := os.ReadFile(filepath.Join(dir, pluginID, manifestName))
	if readErr == nil {
		var m manifest
		if json.Unmarshal(data, &m) == nil {
			backendDir = backendDirectory(m.Backend)
		}
	}
	if backendDir != "" && (assetPath == backendDir || strings.HasPrefix(assetPath, backendDir+"/")) {
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

// ResolveBackend validates and locates a plugin's optional Node backend.
func ResolveBackend(dir, pluginID string) (Backend, error) {
	plugin, err := load(dir, pluginID)
	if err != nil || plugin.BackendURL == "" {
		return Backend{}, os.ErrNotExist
	}
	root := filepath.Join(dir, pluginID)
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil || len(data) > maxManifest {
		return Backend{}, os.ErrNotExist
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var m manifest
	if dec.Decode(&m) != nil || m.ID != pluginID || m.Backend == nil || validBackendEntry(m.Backend.Entry) != nil {
		return Backend{}, os.ErrNotExist
	}
	entry := filepath.Join(root, filepath.FromSlash(m.Backend.Entry))
	if !regularFile(entry) {
		return Backend{}, os.ErrNotExist
	}
	backendRoot := filepath.Join(root, backendDirectory(m.Backend))
	revision, err := backendRevision(backendRoot)
	if err != nil {
		return Backend{}, os.ErrNotExist
	}
	return Backend{PluginID: pluginID, Directory: backendRoot, Entry: entry, Revision: revision}, nil
}

func backendRevision(root string) (string, error) {
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer rootFS.Close()

	hash := sha256.New()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("invalid backend file")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := rootFS.ReadFile(rel)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(rel), info.Size())
		_, _ = hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
}

// MCPServers resolves every manifest-declared MCP server for a workspace.
func MCPServers(dir, workingDir, chatID string, active ...func(string) bool) []adapter.MCPServer {
	var out []adapter.MCPServer
	for _, plugin := range Catalog(dir).Plugins {
		if len(active) > 0 && !active[0](plugin.ID) {
			continue
		}
		root := filepath.Join(dir, plugin.ID)
		data, err := os.ReadFile(filepath.Join(root, manifestName))
		if err != nil {
			continue
		}
		var m manifest
		if json.Unmarshal(data, &m) != nil || m.Backend == nil {
			continue
		}
		for _, server := range m.Backend.MCP {
			env := make([]string, 0, len(server.Env)+1)
			for key, value := range server.Env {
				env = append(env, key+"="+value)
			}
			// A local MCP process is scoped to one live chat, so trusted plugin
			// tools can identify their caller without relying on model-supplied IDs.
			if chatID != "" {
				env = append(env, "DAW_CHAT_ID="+chatID)
			}
			sort.Strings(env)
			cwd := workingDir
			if server.WorkingDir != "" {
				cwd = filepath.Join(workingDir, filepath.FromSlash(server.WorkingDir))
			}
			command := server.Command
			args := append([]string(nil), server.Args...)
			if command != "" && !filepath.IsAbs(command) && strings.Contains(command, "/") {
				command = filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(m.Backend.Entry))), filepath.FromSlash(command))
			}
			name := plugin.ID + "-" + server.ID
			out = append(out, adapter.MCPServer{
				Name: name, Command: command, Args: args, Env: env,
				WorkingDir: cwd, URL: server.URL, Transport: server.Transport, Headers: server.Headers,
			})
		}
	}
	return out
}

// HasWebhook reports whether a valid plugin declares a webhook endpoint.
func HasWebhook(dir, pluginID, webhookID string) bool {
	if !idPattern.MatchString(webhookID) {
		return false
	}
	root := filepath.Join(dir, pluginID)
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil || len(data) > maxManifest {
		return false
	}
	var m manifest
	if json.Unmarshal(data, &m) != nil || m.ID != pluginID || m.Backend == nil {
		return false
	}
	for _, webhook := range m.Backend.Webhooks {
		if webhook.ID == webhookID {
			return true
		}
	}
	return false
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
