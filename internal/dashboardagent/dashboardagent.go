// Package dashboardagent constructs the dashboard's default coding agent
// directly with the docker-agent Go SDK. No YAML agent definition is involved.
package dashboardagent

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/docker/docker-agent/pkg/agent"
	dacfg "github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/config/types"
	"github.com/docker/docker-agent/pkg/model/provider/dmr"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	"github.com/docker/docker-agent/pkg/model/provider/providers"
	"github.com/docker/docker-agent/pkg/skills"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/teamloader"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin/filesystem"
	"github.com/docker/docker-agent/pkg/tools/builtin/shell"
	skillstool "github.com/docker/docker-agent/pkg/tools/builtin/skills"
	mcptools "github.com/docker/docker-agent/pkg/tools/mcp"
	"github.com/rumpl/daw/internal/adapter"
)

// Name is the source name exposed by the dashboard API.
const Name = "dashboard-coder"

//go:embed system_prompt.md
var instruction string

// Build creates a fresh SDK team and model/toolset graph for one working
// directory. The returned LoadResult is the same shape the runtime consumes
// for file-based agents, but every agent option is assembled in Go.
func Build(ctx context.Context, runConfig *dacfg.RuntimeConfig, mcpServers ...adapter.MCPServer) (*teamloader.LoadResult, error) {
	if runConfig == nil {
		runConfig = &dacfg.RuntimeConfig{}
	}
	env := runConfig.EnvProvider()
	modelConfig := dacfg.AutoModelConfig(ctx, runConfig.ModelsGateway, env, runConfig.DefaultModel, dmr.ListModels)
	modelConfig.Name = "auto"

	registry := providers.NewDefaultRegistry()
	modelOpts := []options.Opt{
		options.WithGateway(runConfig.ModelsGateway),
		options.WithProviders(runConfig.Providers),
	}

	if store, err := runConfig.ModelsDevStore(); err == nil {
		modelOpts = append(modelOpts, options.WithModelsDevStore(store))
	}
	model, err := registry.New(ctx, &modelConfig, env, modelOpts...)
	if err != nil {
		return nil, fmt.Errorf("create dashboard coding model: %w", err)
	}

	toolsets := []tools.ToolSet{
		filesystem.New(runConfig.WorkingDir),
		shell.New(os.Environ(), runConfig),
		skillstool.New(skills.Load(ctx, []string{"local"}), runConfig.WorkingDir),
	}
	for _, server := range mcpServers {
		switch {
		case server.Command != "":
			cwd := server.WorkingDir
			if cwd == "" {
				cwd = runConfig.WorkingDir
			}
			toolsets = append(toolsets, mcptools.NewToolsetCommand(server.Name, server.Command, server.Args, append(os.Environ(), server.Env...), cwd))
		case server.URL != "":
			toolsets = append(toolsets, mcptools.NewRemoteToolset(server.Name, server.URL, server.Transport, server.Headers, nil))
		}
	}

	root := agent.New(
		"root",
		instruction,
		agent.WithAddPromptFiles([]string{"AGENTS.md"}),
		agent.WithModel(model),
		agent.WithCommands(types.Commands{
			"commit-and-push": types.Command{
				Description: "Commit and push changes to the current git repository",
				Instruction: "Commit and push all changes in the current git repository. Use a descriptive commit message.",
			},
		}),
		agent.WithAddDate(true),
		agent.WithAddEnvironmentInfo(true),
		agent.WithTools(DeveloperDocumentationTool()),
		agent.WithToolSets(toolsets...),
	)

	models := map[string]latest.ModelConfig{"auto": modelConfig}
	runConfig.Models = models
	runConfig.ProviderRegistry = registry
	return &teamloader.LoadResult{
		Team:               team.New(team.WithAgents(root)),
		Models:             models,
		Providers:          runConfig.Providers,
		ProviderRegistry:   registry,
		AgentDefaultModels: map[string]string{"root": "auto"},
	}, nil
}
