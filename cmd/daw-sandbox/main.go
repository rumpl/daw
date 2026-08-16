// Command daw-sandbox launches the host dashboard's Docker Sandbox execution mode.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
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
	perSession := flag.Bool("per-session", false, "let the host dashboard provision one sandbox per session")
	template := flag.String("template", "", "existing sandbox template image (per-session mode builds one when empty)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *perSession {
		if *dashboard == "" {
			return errors.New("-per-session requires -dashboard")
		}
		workspacePath, err := filepath.Abs(*workspace)
		if err != nil {
			return err
		}
		kitPath, err := filepath.Abs(*kit)
		if err != nil {
			return err
		}
		pluginPath := ""
		if strings.TrimSpace(*pluginDir) != "" {
			pluginPath, err = filepath.Abs(*pluginDir)
			if err != nil {
				return err
			}
		}
		templateRef := strings.TrimSpace(*template)
		if templateRef == "" {
			fmt.Println("ensuring content-addressed daw-runner sandbox template...")
			templateRef, err = sandboxrunner.EnsureTemplate(ctx, sbx.New(), sandboxrunner.TemplateOptions{
				Workspace: workspacePath, Kit: kitPath, CPUs: *cpus, Memory: *memory, Wait: *wait,
			})
			if err != nil {
				return err
			}
		}
		fmt.Printf("sandbox template: %s\n", templateRef)
		return runDashboard(ctx, *dashboard,
			"DAWUI_SANDBOX_PER_SESSION=1",
			"DAWUI_SANDBOX_WORKSPACE="+workspacePath,
			"DAWUI_SANDBOX_KIT="+kitPath,
			"DAWUI_SANDBOX_TEMPLATE="+templateRef,
			"DAWUI_SANDBOX_PLUGIN_DIR="+pluginPath,
			"DAWUI_SANDBOX_CPUS="+strconv.Itoa(*cpus),
			"DAWUI_SANDBOX_MEMORY="+*memory,
			"DAWUI_SANDBOX_WAIT="+wait.String(),
		)
	}
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
	return runDashboard(ctx, *dashboard,
		"DAWUI_RUNNER_URL="+runner.Endpoint,
		"DAWUI_RUNNER_TOKEN="+runner.Token,
		"DAWUI_RUNNER_WORKSPACE="+workspacePath,
	)
}

func runDashboard(ctx context.Context, executable string, values ...string) error {
	hostUser, err := user.Current()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, executable)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	environment := make([]string, 0, len(os.Environ())+len(values)+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			environment = append(environment, entry)
		}
	}
	command.Env = append(environment, "HOME="+hostUser.HomeDir)
	command.Env = append(command.Env, values...)
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
