package sandboxrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sbx "github.com/rumpl/go-sbx"
)

// TemplateOptions controls the one-time sandbox snapshot used as the base for
// new DAW session sandboxes.
type TemplateOptions struct {
	Workspace string
	Kit       string
	CPUs      int
	Memory    string
	Wait      time.Duration
}

type templateList struct {
	Images []struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
	} `json:"images"`
}

// EnsureTemplate returns a content-addressed template containing daw-runner.
// The expensive kit file materialization happens only while producing this
// snapshot; normal session sandboxes apply a lightweight kit with the binary
// omitted.
func EnsureTemplate(ctx context.Context, client *sbx.Client, options TemplateOptions) (string, error) {
	if client == nil {
		return "", errors.New("sandbox runner template: nil sbx client")
	}
	kit, err := existingDirectory(options.Kit)
	if err != nil {
		return "", fmt.Errorf("sandbox runner template: kit: %w", err)
	}
	workspace, err := existingDirectory(options.Workspace)
	if err != nil {
		return "", fmt.Errorf("sandbox runner template: workspace: %w", err)
	}
	digest, err := templateDigest(kit, options.CPUs, options.Memory)
	if err != nil {
		return "", err
	}
	tag := "daw-runner:" + digest[:12]
	exists, err := templateExists(ctx, client, tag)
	if err != nil {
		return "", err
	}
	if exists {
		return tag, nil
	}
	if options.Wait <= 0 {
		options.Wait = 2 * time.Minute
	}

	seedName := "daw-template-build-" + digest[:12]
	_, _ = client.Command(ctx, "rm", "-f", seedName)
	_ = RemoveToken(seedName)
	defer func() {
		_, _ = client.Command(context.WithoutCancel(ctx), "rm", "-f", seedName)
		_ = RemoveToken(seedName)
	}()

	_, err = Start(ctx, client, Options{
		Workspace: workspace, Kit: kit, Name: seedName,
		CPUs: options.CPUs, Memory: options.Memory, SkipRunner: true,
	})
	if err != nil {
		return "", fmt.Errorf("sandbox runner template: create seed: %w", err)
	}
	// Do not snapshot process state, a session database, or the throwaway seed
	// token. The executable and startup script remain in the filesystem image.
	const clean = `
set -eu
pid_file=/home/agent/.cagent/daw-runner/runner.pid
if [ -s "$pid_file" ]; then
  kill "$(cat "$pid_file")" 2>/dev/null || true
fi
rm -rf /home/agent/.cagent/daw-runner
rm -f /home/agent/.config/daw/runner-token
`
	if _, err := client.Command(ctx, "exec", seedName, "--", "sh", "-lc", clean); err != nil {
		return "", fmt.Errorf("sandbox runner template: clean seed: %w", err)
	}
	if _, err := client.Command(ctx, "stop", seedName); err != nil {
		return "", fmt.Errorf("sandbox runner template: stop seed: %w", err)
	}
	if _, err := client.Command(ctx, "template", "save", seedName, tag); err != nil {
		if exists, checkErr := templateExists(ctx, client, tag); checkErr == nil && exists {
			return tag, nil
		}
		return "", fmt.Errorf("sandbox runner template: save %q: %w", tag, err)
	}
	return tag, nil
}

func templateExists(ctx context.Context, client *sbx.Client, reference string) (bool, error) {
	repository, tag, found := strings.Cut(reference, ":")
	if !found || repository == "" || tag == "" {
		return false, fmt.Errorf("sandbox runner template: invalid reference %q", reference)
	}
	result, err := client.Command(ctx, "template", "ls", "--json")
	if err != nil {
		return false, fmt.Errorf("sandbox runner template: list: %w", err)
	}
	var list templateList
	if err := json.Unmarshal([]byte(result.Stdout), &list); err != nil {
		return false, fmt.Errorf("sandbox runner template: decode list: %w", err)
	}
	for _, image := range list.Images {
		actualRepository := strings.TrimPrefix(image.Repository, "docker.io/library/")
		if actualRepository == repository && image.Tag == tag {
			return true, nil
		}
	}
	return false, nil
}

func templateDigest(kit string, cpus int, memory string) (string, error) {
	hash := sha256.New()
	if cpus != 0 || strings.TrimSpace(memory) != "" {
		_, _ = fmt.Fprintf(hash, "resource-override: cpus=%d memory=%s\n", cpus, memory)
	}
	paths := []string{
		filepath.Join(kit, "spec.yaml"),
		filepath.Join(kit, "files", "home", ".local", "lib", "daw-runner"),
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("sandbox runner template: hash %s: %w", path, err)
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("sandbox runner template: hash %s: %w", path, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("sandbox runner template: close %s: %w", path, closeErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
