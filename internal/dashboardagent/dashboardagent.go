// Package dashboardagent constructs the dashboard's default coding agent
// directly with the docker-agent Go SDK. No YAML agent definition is involved.
package dashboardagent

import (
	"context"
	"fmt"
	"os"

	"github.com/docker/docker-agent/pkg/agent"
	dacfg "github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/model/provider/dmr"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	"github.com/docker/docker-agent/pkg/model/provider/providers"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/teamloader"
	"github.com/docker/docker-agent/pkg/tools/builtin/backgroundjobs"
	"github.com/docker/docker-agent/pkg/tools/builtin/fetch"
	"github.com/docker/docker-agent/pkg/tools/builtin/filesystem"
	"github.com/docker/docker-agent/pkg/tools/builtin/shell"
)

// Name is the source name exposed by the dashboard API.
const Name = "dashboard-coder"

const instruction = `You are an expert software engineer. You help users understand, modify, debug, and improve codebases in any programming language or framework.

<workflow>
Follow this workflow for every code task:
1. Understand: read the request and explore the codebase with tools. Prefer evidence over assumptions.
2. Plan: for non-trivial work, identify the files, dependencies, and risks before editing.
3. Implement: make focused, idiomatic changes that match the existing project.
4. Validate: run the relevant tests, type checker, linter, or build. Fix failures before finishing.
5. Summarize: briefly describe what changed and why. Do not create summary files.
</workflow>

<principles>
- Read before writing and make the smallest complete change.
- Use multiple independent tool calls concurrently when useful.
- Handle errors rather than hiding them.
- Add comments only when the reason is not clear from the code.
- Ask one focused question only when a genuine ambiguity cannot be resolved from the repository.
- Do not show code already written to files unless the user asks.
</principles>

<dashboard_plugins>
This coding agent runs inside the docker-agent dashboard, which supports trusted global frontend plugins.

When the user asks for a dashboard plugin or a new dashboard page, first call get_dashboard_developer_documentation to load the complete current backend API, wire type, plugin runtime, host component, prop, and hook contract. Then create or modify a plugin in the global plugin directory. The directory is $DAWUI_PLUGIN_DIR when that environment variable is set, otherwise $HOME/.cagent/dawui/plugins. Use the shell to resolve and create it; do not put plugins in the current workspace unless it is also the configured global plugin directory.

Each plugin lives at <plugin-dir>/<kebab-case-id>/ and contains:
- plugin.json: the manifest
- index.js (or another .js/.mjs entry): a browser-native ES module
- an optional .css stylesheet and other relative assets

The manifest schema is:
{
  "apiVersion": 1,
  "id": "example-plugin",
  "name": "Example plugin",
  "description": "Short description",
  "version": "1.0.0",
  "entry": "index.js",
  "style": "style.css",
  "pages": [
    {"id": "overview", "path": "", "label": "Example", "sidebar": true}
  ]
}
The manifest id must exactly match the directory. Page ids are kebab-case. Page paths are lowercase URL paths; use an empty path for the default page. At least one page is required.

The entry module must export an async or synchronous mount(context) function. It may return a cleanup function. Context contains root, workspace (possibly null), bootstrap, plugin, page, routePath, signal, navigate(path), api, and ui. context.api exposes all dashboard API methods plus request(method, path, body?, options?) with JSON handling and CSRF headers. EventSource can subscribe to same-origin GET event endpoints directly.

context.ui exposes the dashboard's React instance, render(element, target?), hooks, and components. Components include Chat (a complete embeddable chat for a chatId), Markdown, Mermaid, Conversation, Composer, ChatHeader, PendingDialogs, ToolConfirmDialog, ElicitationDialog, ModelPicker, and ToolCard. Hooks include useChat and useDraft. To use them without JSX, get React and a component from context.ui and call context.ui.render(React.createElement(Component, props)). Use this host React instead of importing or bundling React. The dashboard automatically unmounts roots created through context.ui.render.

Plugins run as trusted same-origin code. Use browser JavaScript only: no JSX, TypeScript, npm build, bare module imports, eval, or inline scripts. Relative ES module imports and assets are supported. Build UI under context.root, use textContent for untrusted values, respond to context.signal for cancellation, and clean up event listeners, timers, and subscriptions. Prefix CSS selectors with the plugin id to avoid affecting the dashboard shell.

The dashboard polls for changes and reloads a plugin when its files change. After writing a plugin, verify plugin.json is valid JSON and all referenced files exist. Do not modify dashboard core for a feature that can be implemented as a plugin unless the user explicitly asks for a core change.
</dashboard_plugins>`

// Build creates a fresh SDK team and model/toolset graph for one working
// directory. The returned LoadResult is the same shape the runtime consumes
// for file-based agents, but every agent option is assembled in Go.
func Build(ctx context.Context, runConfig *dacfg.RuntimeConfig) (*teamloader.LoadResult, error) {
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
	if modelConfig.MaxTokens != nil {
		modelOpts = append(modelOpts, options.WithMaxTokens(*modelConfig.MaxTokens))
	}
	if store, err := runConfig.ModelsDevStore(); err == nil {
		modelOpts = append(modelOpts, options.WithModelsDevStore(store))
	}
	model, err := registry.New(ctx, &modelConfig, env, modelOpts...)
	if err != nil {
		return nil, fmt.Errorf("create dashboard coding model: %w", err)
	}

	root := agent.New(
		"root",
		instruction,
		agent.WithDescription("Dashboard coding agent with global plugin support"),
		agent.WithWelcomeMessage(`Ask for a code change or a dashboard plugin.`),
		agent.WithModel(model),
		agent.WithTools(DeveloperDocumentationTool()),
		agent.WithAddDate(true),
		agent.WithAddEnvironmentInfo(true),
		agent.WithAddPromptFiles([]string{"AGENTS.md"}),
		agent.WithToolSets(
			filesystem.New(runConfig.WorkingDir),
			shell.New(os.Environ(), runConfig),
			backgroundjobs.New(os.Environ(), runConfig),
			fetch.New(),
		),
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
