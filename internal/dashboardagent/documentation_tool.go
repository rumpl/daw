package dashboardagent

import (
	"context"
	_ "embed"

	"github.com/docker/docker-agent/pkg/tools"
)

//go:embed developer_documentation.md
var developerDocumentation string

type developerDocumentationArgs struct{}

// DeveloperDocumentation returns the exact reference exposed by the default
// agent's documentation tool.
func DeveloperDocumentation() string { return developerDocumentation }

// DeveloperDocumentationTool gives the code-built default agent a stable,
// read-only way to retrieve the complete plugin-facing backend and frontend
// contract without searching this repository or relying on prompt memory.
func DeveloperDocumentationTool() tools.Tool {
	return tools.Tool{
		Name:        "get_dashboard_developer_documentation",
		Category:    "documentation",
		Description: "Return the complete, current developer documentation for the dashboard backend HTTP/SSE API, wire types, plugin runtime, API client, React components, component props, and hooks. Call this before creating or changing a dashboard plugin.",
		Parameters:  tools.MustSchemaFor[developerDocumentationArgs](),
		Annotations: tools.ToolAnnotations{
			Title:        "Dashboard developer documentation",
			ReadOnlyHint: true,
		},
		Handler: tools.NewHandler(func(context.Context, developerDocumentationArgs) (*tools.ToolCallResult, error) {
			return tools.ResultSuccess(developerDocumentation), nil
		}),
	}
}
