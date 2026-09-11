package pluginoci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/rumpl/daw/internal/plugins"
	"github.com/rumpl/daw/internal/protocol"
)

const (
	ConfigMediaType = types.MediaType("application/vnd.atelier.plugin.config.v1+json")
	LayerMediaType  = types.MediaType("application/vnd.atelier.plugin.layer.v1.tar+gzip")
	maxArchiveSize  = 256 << 20
	maxFiles        = 20_000
)

// Image validates a plugin directory and packs it as a deterministic OCI image.
func Image(directory string) (v1.Image, protocol.Plugin, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, protocol.Plugin{}, err
	}
	id := filepath.Base(directory)
	catalog := plugins.Catalog(filepath.Dir(directory))
	var plugin protocol.Plugin
	for _, candidate := range catalog.Plugins {
		if candidate.ID == id {
			plugin = candidate
			break
		}
	}
	if plugin.ID == "" {
		for _, diagnostic := range catalog.Errors {
			if diagnostic.PluginID == id {
				return nil, protocol.Plugin{}, fmt.Errorf("invalid plugin: %s", diagnostic.Message)
			}
		}
		return nil, protocol.Plugin{}, errors.New("directory is not a valid plugin")
	}

	archive, err := archiveDirectory(directory)
	if err != nil {
		return nil, protocol.Plugin{}, err
	}
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, ConfigMediaType)
	img, err = mutate.Append(img, mutate.Addendum{Layer: static.NewLayer(archive, LayerMediaType), MediaType: LayerMediaType})
	if err != nil {
		return nil, protocol.Plugin{}, err
	}
	img = mutate.Annotations(img, map[string]string{
		"org.opencontainers.image.title":   plugin.Name,
		"org.opencontainers.image.version": plugin.Version,
		"io.atelier.plugin.id":             plugin.ID,
	}).(v1.Image)
	return img, plugin, nil
}

// Push packs directory and publishes it using the credentials understood by Docker.
func Push(ctx context.Context, directory, reference string) (name.Reference, v1.Hash, error) {
	ref, err := name.ParseReference(reference)
	if err != nil {
		return nil, v1.Hash{}, fmt.Errorf("invalid OCI reference: %w", err)
	}
	img, _, err := Image(directory)
	if err != nil {
		return nil, v1.Hash{}, err
	}
	if err := remote.Write(ref, img, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return nil, v1.Hash{}, fmt.Errorf("push %s: %w", ref.Name(), err)
	}
	digest, err := img.Digest()
	return ref, digest, err
}

// Install pulls an Atelier plugin artifact and atomically installs and enables it.
func Install(ctx context.Context, pluginDir, reference string) (protocol.Plugin, error) {
	ref, err := name.ParseReference(strings.TrimSpace(reference))
	if err != nil {
		return protocol.Plugin{}, fmt.Errorf("invalid OCI reference: %w", err)
	}
	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return protocol.Plugin{}, fmt.Errorf("pull %s: %w", ref.Name(), err)
	}
	manifest, err := img.Manifest()
	if err != nil {
		return protocol.Plugin{}, fmt.Errorf("read artifact manifest: %w", err)
	}
	if manifest.Config.MediaType != ConfigMediaType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != LayerMediaType {
		return protocol.Plugin{}, errors.New("reference is not an Atelier plugin artifact")
	}
	layers, err := img.Layers()
	if err != nil || len(layers) != 1 {
		return protocol.Plugin{}, errors.New("plugin artifact layer could not be read")
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		return protocol.Plugin{}, fmt.Errorf("open plugin archive: %w", err)
	}
	defer reader.Close()

	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return protocol.Plugin{}, err
	}
	stage, err := os.MkdirTemp(pluginDir, ".install-*")
	if err != nil {
		return protocol.Plugin{}, err
	}
	defer os.RemoveAll(stage)
	payload := filepath.Join(stage, "payload")
	if err := os.Mkdir(payload, 0o700); err != nil {
		return protocol.Plugin{}, err
	}
	if err := extractArchive(reader, payload); err != nil {
		return protocol.Plugin{}, err
	}
	manifestData, err := os.ReadFile(filepath.Join(payload, "plugin.json"))
	if err != nil {
		return protocol.Plugin{}, errors.New("plugin archive has no plugin.json")
	}
	var identity struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(manifestData, &identity) != nil || identity.ID == "" {
		return protocol.Plugin{}, errors.New("plugin archive has an invalid plugin.json")
	}
	candidate := filepath.Join(stage, identity.ID)
	if err := os.Rename(payload, candidate); err != nil {
		return protocol.Plugin{}, err
	}
	catalog := plugins.Catalog(stage)
	if len(catalog.Plugins) != 1 || catalog.Plugins[0].ID != identity.ID || len(catalog.Errors) != 0 {
		message := "plugin artifact is invalid"
		if len(catalog.Errors) > 0 {
			message += ": " + catalog.Errors[0].Message
		}
		return protocol.Plugin{}, errors.New(message)
	}

	destination := filepath.Join(pluginDir, identity.ID)
	backup := filepath.Join(stage, ".previous")
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return protocol.Plugin{}, fmt.Errorf("replace existing plugin: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.Plugin{}, err
	}
	if err := os.Rename(candidate, destination); err != nil {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			_ = os.Rename(backup, destination)
		}
		return protocol.Plugin{}, fmt.Errorf("install plugin: %w", err)
	}
	installed := plugins.Catalog(pluginDir)
	for _, plugin := range installed.Plugins {
		if plugin.ID == identity.ID {
			return plugin, nil
		}
	}
	return protocol.Plugin{}, errors.New("installed plugin could not be loaded")
}

func archiveDirectory(root string) ([]byte, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("plugin symlinks are not supported")
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		if len(paths) > maxFiles {
			return errors.New("plugin contains too many files")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gzipWriter)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("plugins may contain only directories and regular files")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		header := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(header); err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if out.Len() > maxArchiveSize {
			return nil, errors.New("plugin archive exceeds 256 MiB")
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func extractArchive(reader io.Reader, destination string) error {
	gzipReader, err := gzip.NewReader(io.LimitReader(reader, maxArchiveSize+1))
	if err != nil {
		return fmt.Errorf("open plugin archive: %w", err)
	}
	defer gzipReader.Close()
	seen := map[string]bool{}
	tr := tar.NewReader(gzipReader)
	files, total := 0, int64(0)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read plugin archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("plugin archive may contain only regular files")
		}
		if strings.Contains(header.Name, "\\") || !fs.ValidPath(header.Name) || strings.HasPrefix(header.Name, ".") || filepath.IsAbs(header.Name) {
			return errors.New("plugin archive contains an unsafe path")
		}
		cleanName := strings.ToLower(header.Name)
		if seen[cleanName] {
			return errors.New("plugin archive contains duplicate paths")
		}
		seen[cleanName] = true
		if header.Size < 0 || header.Size > maxArchiveSize-total || files >= maxFiles {
			return errors.New("plugin archive exceeds installation limits")
		}
		files++
		total += header.Size
		path := filepath.Join(destination, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, tr, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
