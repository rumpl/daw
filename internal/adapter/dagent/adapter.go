// Package dagent is the production adapter: it embeds the matched
// docker-agent Go SDK in-process.
//
// Why the raw runtime rather than pkg/embeddedchat: embeddedchat's compact
// Event type projects only text, tool activity, error and Done. This dashboard
// needs the full stream — reasoning deltas, elicitation IDs for correlation,
// sub-agent transfers, token/cost, compaction, retries, toolset warnings and
// per-session permission control — so it subscribes to runtime.RunStream
// directly, exactly as pkg/cli and the TUI do.
//
// There is NO sandbox here. The runtime executes tools in this process's
// context, with the permissions of the user running the server.
package dagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dacfg "github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/permissions"
	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/runtime/jscommands"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/session/sqlitestore"
	"github.com/docker/docker-agent/pkg/teamloader"
	loaderdefaults "github.com/docker/docker-agent/pkg/teamloader/defaults"
	"github.com/docker/docker-agent/pkg/tools/mcp/keyringstore"
	"github.com/docker/docker-agent/pkg/userconfig"
	"github.com/docker/docker-agent/pkg/version"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/dashboardagent"
	"github.com/rumpl/daw/internal/protocol"
)

// registerOnce enforces the matched module's documented call-order
// constraints: keyringstore.Register() must run before any remote MCP toolset
// is constructed, and jscommands.Register() before commands using ${...}
// expressions are resolved. Both happen here, before the first team load.
var registerOnce sync.Once

// Adapter owns the single process-wide session store and the shared runtime
// configuration.
type Adapter struct {
	log       *slog.Logger
	store     session.Store
	storePath string

	globalPerms *permissions.Checker
}

// Config configures the production adapter.
type Config struct {
	Logger *slog.Logger
	// SessionDB overrides the session database path. Empty means
	// <paths.GetDataDir()>/session.db, i.e. docker-agent's own default.
	SessionDB string
}

// New opens docker-agent's native SQLite session store exactly once and reads
// the user's global configuration through the normal loading path.
func New(ctx context.Context, cfg Config) (*Adapter, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	registerOnce.Do(func() {
		keyringstore.Register()
		jscommands.Register()
	})

	dbPath := cfg.SessionDB
	if dbPath == "" {
		dbPath = filepath.Join(paths.GetDataDir(), "session.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}
	store, err := sqlitestore.New(context.WithoutCancel(ctx), dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening session store: %w", err)
	}

	a := &Adapter{log: log, store: store, storePath: dbPath}
	// The user's global permission configuration (~/.config/cagent/config.yaml)
	// is merged into every team, exactly as the CLI does.
	if settings := userconfig.Get(); settings != nil && settings.Permissions != nil {
		a.globalPerms = permissions.NewChecker(settings.Permissions)
	}
	return a, nil
}

// Info reports non-secret status. It never reads or echoes environment values.
func (a *Adapter) Info(ctx context.Context) (adapter.Info, error) {
	// A released module carries no commit stamp; report it as empty rather
	// than echoing the library's "unknown" placeholder.
	commit := version.Commit
	if commit == "unknown" {
		commit = ""
	}
	info := adapter.Info{
		AgentVersion: version.Version,
		AgentCommit:  commit,
		ConfigDir:    paths.GetConfigDir(),
		DataDir:      paths.GetDataDir(),
		CacheDir:     paths.GetCacheDir(),
		SessionDB:    a.storePath,
	}
	// Model availability is probed through docker-agent's own resolution by
	// loading nothing more than the user's model configuration; a real check
	// happens when a chat opens. We report a hint instead of guessing.
	cfgPath := filepath.Join(paths.GetConfigDir(), "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		info.Notices = append(info.Notices, protocol.Notice{
			ID: "no-user-config", Level: protocol.NoticeInfo, Code: "no_user_config",
			Message: "No ~/.config/cagent/config.yaml found. If no model resolves, run `docker agent setup`.",
		})
	}
	info.BuiltinAgents = append(dacfg.BuiltinAgentNames(), dashboardagent.Name)
	info.ModelsAvailable = true
	info.ModelsHint = "Models are resolved by docker-agent itself; run `docker agent doctor` if a chat reports none."
	_ = ctx
	return info, nil
}

// runtimeConfig builds the per-load runtime configuration. Credentials are
// never touched here: docker-agent's own environment provider and credential
// helpers resolve them inside the SDK.
func (a *Adapter) runtimeConfig(workingDir string) *dacfg.RuntimeConfig {
	rc := &dacfg.RuntimeConfig{}
	rc.WorkingDir = workingDir
	if config, err := userconfig.Load(); err == nil && config != nil {
		rc.ModelsGateway = config.ModelsGateway
		rc.Providers = config.Providers
		if config.DefaultModel != nil {
			model := config.DefaultModel.ModelConfig
			rc.DefaultModel = &model
		}
	}
	return rc
}

func (a *Adapter) load(ctx context.Context, ref string, kind protocol.AgentSourceKind, runConfig *dacfg.RuntimeConfig) (*teamloader.LoadResult, error) {
	if kind == protocol.AgentSourceBuiltin && ref == dashboardagent.Name {
		return dashboardagent.Build(ctx, runConfig)
	}
	src, err := a.source(kind, ref)
	if err != nil {
		return nil, err
	}
	return teamloader.LoadWithConfig(ctx, src, runConfig, loaderdefaults.Opts()...)
}

// source builds the config.Source for an agent reference.
//
// Built-ins go through docker-agent's own ResolveSources so that embedded
// agents AND user aliases pointing at them resolve exactly as `docker agent
// run coder` resolves them.
func (a *Adapter) source(kind protocol.AgentSourceKind, ref string) (dacfg.Source, error) {
	switch kind {
	case protocol.AgentSourceOCI:
		return dacfg.NewOCISource(ref), nil
	case protocol.AgentSourceBuiltin:
		sources, err := dacfg.ResolveSources(ref, a.runtimeConfig("").EnvProvider())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", adapter.ErrInvalidAgent, err)
		}
		for _, src := range sources {
			return src, nil
		}
		return nil, fmt.Errorf("%w: built-in agent %q resolved to nothing", adapter.ErrInvalidAgent, ref)
	default:
		return dacfg.NewFileSource(ref), nil
	}
}

// ResolveAgent loads the team through the CLI's own loader so agent YAML,
// sub-agents, toolsets, models and warnings resolve identically, then reports
// what the configuration declares *before* it is ever run.
func (a *Adapter) ResolveAgent(ctx context.Context, req adapter.ResolveRequest) (*protocol.ResolvedAgent, error) {
	if req.Kind == protocol.AgentSourceOCI && !req.AllowRemoteFetch {
		return nil, adapter.ErrRemoteFetch
	}
	loadCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	res, err := a.load(loadCtx, req.Source, req.Kind, a.runtimeConfig(req.WorkingDir))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", adapter.ErrInvalidAgent, err)
	}
	t := res.Team
	defer func() {
		// Resolution is a preview: stop the toolsets it started.
		if err := t.StopToolSets(context.WithoutCancel(ctx)); err != nil {
			a.log.Debug("stopping preview toolsets", "error", err)
		}
	}()

	out := &protocol.ResolvedAgent{
		Source: req.Source, Kind: req.Kind, Label: label(req.Source),
	}
	defaultAgent, _ := t.DefaultAgent()
	for _, name := range t.AgentNames() {
		ag, err := t.Agent(name)
		if err != nil || ag == nil {
			continue
		}
		d := protocol.AgentDescriptor{
			Name: ag.Name(), Description: ag.Description(),
			IsDefault: defaultAgent != nil && defaultAgent.Name() == ag.Name(),
		}
		if m := ag.Model(ctx); m != nil {
			d.Model = m.ID().String()
		}
		for _, sub := range ag.SubAgents() {
			d.SubAgents = append(d.SubAgents, sub.Name())
		}
		out.Agents = append(out.Agents, d)
		out.Warnings = append(out.Warnings, ag.DrainWarnings()...)
	}

	// Declared toolsets are a trust signal: show them before the first run.
	if cfg, ok := t.AgentConfig(agentNameOrDefault(t, "")); ok {
		for _, ts := range cfg.Toolsets {
			info := protocol.ToolsetInfo{Kind: ts.Type}
			switch ts.Type {
			case "shell":
				info.Detail = "runs shell commands on this host as your user"
			case "filesystem":
				info.Detail = "reads and writes files"
			case "mcp":
				info.Detail = "external MCP server"
				info.Command = strings.TrimSpace(ts.Command + " " + strings.Join(ts.Args, " "))
				if ts.Remote.URL != "" {
					info.Command = ts.Remote.URL
				}
			default:
				info.Detail = ts.Type
			}
			out.Toolsets = append(out.Toolsets, info)
		}
	}

	checker := t.Permissions()
	if a.globalPerms != nil && !a.globalPerms.IsEmpty() {
		checker = permissions.Merge(checker, a.globalPerms)
	}
	// A resolve preview reports the config's own patterns; the effective mode
	// is a per-session property and is reported by the chat itself.
	out.Permissions = viewFromChecker(checker, "", false, nil)
	return out, nil
}

func agentNameOrDefault(t interface {
	AgentNames() []string
}, name string) string {
	if name != "" {
		return name
	}
	names := t.AgentNames()
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

func label(src string) string {
	if i := strings.LastIndexAny(src, "/\\"); i >= 0 {
		return src[i+1:]
	}
	return src
}

func viewFromChecker(c *permissions.Checker, posture protocol.Posture, autoApproveAll bool, grants []string) protocol.PermissionsView {
	v := protocol.PermissionsView{Posture: posture, AutoApproveAll: autoApproveAll, SessionGrants: grants}
	if c != nil {
		v.Allow = c.AllowPatterns()
		v.Ask = c.AskPatterns()
		v.Deny = c.DenyPatterns()
	}
	return v
}

// ListSessions reads docker-agent's lightweight summary query. Only IDs
// returned here are ever accepted for resume. GetSessions must not be used for
// this endpoint: it loads and JSON-decodes every item in every session before
// we discard all but the title, count and timestamps.
func (a *Adapter) ListSessions(ctx context.Context, workingDir string) ([]protocol.SessionSummary, error) {
	summaries, err := a.store.GetSessionSummaries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.SessionSummary, 0, len(summaries))
	for _, summary := range summaries {
		if workingDir != "" && summary.WorkingDir != "" && summary.WorkingDir != workingDir {
			continue
		}
		out = append(out, protocol.SessionSummary{
			SessionID:  summary.ID,
			Title:      summary.Title,
			WorkingDir: summary.WorkingDir,
			CreatedAt:  summary.CreatedAt.UTC().Format(time.RFC3339),
			Messages:   summary.NumMessages,
		})
	}
	return out, nil
}

// OpenChat builds one runtime + session pair. It mirrors the CLI's wiring in
// cmd/root/run.go (createLocalRuntimeAndSession) so toolsets, models, hooks,
// budgets and permissions behave identically.
func (a *Adapter) OpenChat(ctx context.Context, req adapter.OpenRequest) (adapter.Chat, error) {
	runConfig := a.runtimeConfig(req.WorkingDir)
	loadRes, err := a.load(ctx, req.Source, req.Kind, runConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", adapter.ErrInvalidAgent, err)
	}
	t := loadRes.Team

	// Merge the user's global permissions and the workspace .agentsignore
	// into the team checker, exactly as the CLI does.
	if a.globalPerms != nil && !a.globalPerms.IsEmpty() {
		t.SetPermissions(permissions.Merge(t.Permissions(), a.globalPerms))
	}
	agentsIgnore := false
	if rules := permissions.FromAgentsIgnore(req.WorkingDir); rules != nil {
		t.SetPermissions(permissions.Merge(t.Permissions(), rules))
		agentsIgnore = true
	}

	ag, err := t.AgentOrDefault(req.AgentName)
	if err != nil {
		_ = t.StopToolSets(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("%w: %v", adapter.ErrInvalidAgent, err)
	}
	agentName := ag.Name()

	modelSwitcher := &daruntime.ModelSwitcherConfig{
		Models:             loadRes.Models,
		Providers:          loadRes.Providers,
		ModelsGateway:      runConfig.ModelsGateway,
		EnvProvider:        runConfig.EnvProvider(),
		ProviderRegistry:   loadRes.ProviderRegistry,
		AgentDefaultModels: loadRes.AgentDefaultModels,
	}
	if ms, err := runConfig.ModelsDevStore(); err == nil {
		modelSwitcher.ModelsStore = ms
	}

	rt, err := daruntime.New(ctx, t,
		daruntime.WithSessionStore(a.store),
		daruntime.WithCurrentAgent(agentName),
		daruntime.WithWorkingDir(req.WorkingDir),
		daruntime.WithModelSwitcherConfig(modelSwitcher),
		daruntime.WithBudget(loadRes.Budget),
		daruntime.WithNamedBudgets(loadRes.Budgets, loadRes.AgentBudgets),
		daruntime.WithRetryOnRateLimit(),
	)
	if err != nil {
		_ = t.StopToolSets(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("creating runtime: %w", err)
	}

	var sess *session.Session
	newSession := false
	if req.ResumeSessionID != "" {
		sess, err = a.store.GetSession(ctx, req.ResumeSessionID)
		if err != nil {
			_ = rt.Close()
			_ = t.StopToolSets(context.WithoutCancel(ctx))
			if errors.Is(err, session.ErrNotFound) {
				return nil, adapter.ErrNotFound
			}
			return nil, err
		}
		// Reapply stored per-agent model overrides, as the CLI does.
		if len(sess.AgentModelOverrides) > 0 && rt.SupportsModelSwitching() {
			for name, ref := range sess.AgentModelOverrides {
				if err := rt.SetAgentModel(ctx, name, ref); err != nil {
					a.log.Warn("reapply model override failed", "agent", name)
				}
			}
		}
	} else {
		sess = session.New(
			session.WithMaxIterations(ag.MaxIterations()),
			session.WithMaxConsecutiveToolCalls(ag.MaxConsecutiveToolCalls()),
			session.WithMaxOldToolCallTokens(ag.MaxOldToolCallTokens()),
			session.WithMaxToolResultTokens(ag.MaxToolResultTokens()),
			session.WithWorkingDir(req.WorkingDir),
			session.WithTitle(placeholderTitle),
		)
		// Like the CLI, the session row is created lazily on the first real
		// message so browsing the dashboard never litters the user's store
		// with empty sessions.
		newSession = true
	}

	c := &chat{
		a: a, rt: rt, team: t, sess: sess, agentName: agentName,
		workingDir: req.WorkingDir, source: req.Source, kind: req.Kind,
		events:       make(chan protocol.Event, 512),
		unsaved:      newSession,
		pendingTools: map[string]pendingTool{},
		partialTools: map[string]partialTool{},
		pendingElic:  map[string]struct{}{},
		agentsIgnore: agentsIgnore,
		run:          protocol.RunStatus{State: protocol.RunStateIdle},
	}
	// An empty posture means "keep whatever safety mode this session already
	// carries" (resume). A new chat is given the server's configured default.
	if req.Posture != "" {
		c.applyPosture(req.Posture)
	} else {
		c.mu.Lock()
		c.posture = postureFor(sess.GetSafetyPolicy())
		c.mu.Unlock()
	}
	c.refreshQueue()
	c.startBackgroundBridges()
	c.collectWarnings(ag)

	// Restore dashboard choices before startup metadata is emitted. Preferences
	// can become stale when an agent config or provider catalog changes; in that
	// case opening the session is more useful than failing it, so retain the
	// runtime's resolved value and log the rejected preference.
	if req.Model != "" {
		if err := c.SetModel(ctx, req.Model); err != nil {
			a.log.Warn("restore model preference failed", "session", sess.ID)
		}
	}
	if req.ThinkingLevel != "" {
		if err := c.SetThinking(ctx, req.ThinkingLevel); err != nil {
			a.log.Warn("restore thinking preference failed", "session", sess.ID)
		}
	}

	// Emit the runtime's own startup info so model, thinking level, team and
	// toolset state are populated before the first turn (and token usage is
	// restored for a resumed session), instead of being blank until the user
	// sends something.
	go func() {
		sink := daruntime.EventSinkFunc(func(ev daruntime.Event) { c.normalize(ev) })
		c.rt.EmitStartupInfo(context.WithoutCancel(ctx), sess, sink)
	}()

	return c, nil
}

// Close closes the shared session store. Individual chats own their runtimes.
func (a *Adapter) Close() error { return a.store.Close() }
