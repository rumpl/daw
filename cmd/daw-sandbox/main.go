// Command daw-sandbox starts the sandbox-local runner used by the dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rumpl/daw/internal/sandboxrunner"
	sbx "github.com/rumpl/go-sbx"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	workspace := flag.String("workspace", ".", "host workspace to mount in the sandbox")
	kit := flag.String("kit", defaultKitPath(), "path to the daw-runner kit")
	pluginDir := flag.String("plugins", defaultPluginDir(), "host plugin directory to mount (empty disables plugins)")
	name := flag.String("name", "", "sandbox name (default: stable name derived from workspace)")
	cpus := flag.Int("cpus", 0, "sandbox CPU count (0 uses the kit/default)")
	memory := flag.String("memory", "", "sandbox memory limit, for example 8g")
	wait := flag.Duration("wait", 2*time.Minute, "maximum time to wait for the runner API")
	dashboard := flag.String("dashboard", "", "host dashboard executable to run after the sandbox is ready")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := sbx.New()
	runner, err := sandboxrunner.Start(ctx, client, sandboxrunner.Options{
		Workspace: *workspace, Kit: *kit, PluginDir: *pluginDir,
		Name: *name, CPUs: *cpus, Memory: *memory,
	})
	if err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, *wait)
	defer cancel()
	if err := sandboxrunner.WaitReady(readyCtx, runner.Endpoint, runner.Token); err != nil {
		return err
	}
	if err := sandboxrunner.PrimeCredentials(readyCtx, client, runner.Name); err != nil {
		return err
	}

	fmt.Printf("sandbox: %s\n", runner.Name)
	fmt.Printf("runner:  %s\n", runner.Endpoint)
	if *dashboard == "" {
		fmt.Printf("start host: DAWUI_RUNNER_URL=%q DAWUI_RUNNER_TOKEN=<redacted> DAWUI_RUNNER_WORKSPACE=%q make start\n", runner.Endpoint, *workspace)
		return nil
	}
	workspacePath, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	hostUser, err := user.Current()
	if err != nil {
		return err
	}
	hostHome := hostUser.HomeDir
	command := exec.CommandContext(ctx, *dashboard)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	environment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			environment = append(environment, entry)
		}
	}
	command.Env = append(environment,
		"HOME="+hostHome,
		"DAWUI_RUNNER_URL="+runner.Endpoint,
		"DAWUI_RUNNER_TOKEN="+runner.Token,
		"DAWUI_RUNNER_WORKSPACE="+workspacePath,
	)
	return command.Run()
}

func defaultPluginDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".cagent", "dawui", "plugins")
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return ""
}

func defaultKitPath() string {
	executable, err := os.Executable()
	if err == nil {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "kits", "daw-runner"))
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join("kits", "daw-runner")
}
