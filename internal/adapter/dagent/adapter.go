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
	"sync"
	"time"

	dacfg "github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/effort"
	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/permissions"
	daruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/runtime/jscommands"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/session/sqlitestore"
	"github.com/docker/docker-agent/pkg/tools/mcp/keyringstore"
	"github.com/docker/docker-agent/pkg/userconfig"
	"github.com/docker/docker-agent/pkg/version"
	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/dashboardagent"
	"github.com/rumpl/daw/internal/protocol"
	"github.com/rumpl/daw/internal/sessionlineage"
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
	log.Info("session store opened", "path", dbPath)

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
	info.ModelsAvailable = true
	info.ModelsHint = "Models are resolved by docker-agent itself; run `docker agent doctor` if a chat reports none."
	_ = ctx
	return info, nil
}

// runtimeConfig builds the per-load runtime configuration. Credentials are
// never touched here: docker-agent's own environment provider and credential
// helpers resolve them inside the SDK.
// ChatOptions resolves the global model catalog without creating a workspace
// chat or allocating a session. docker-agent currently owns catalog discovery
// on LocalRuntime, so this constructs a short-lived, model-only runtime from
// global user configuration; the working directory is deliberately not part
// of model selection.
func (a *Adapter) ChatOptions(ctx context.Context, model string) ([]protocol.ModelOption, []string, error) {
	workingDir, err := os.UserHomeDir()
	if err != nil {
		workingDir = os.TempDir()
	}
	runConfig := a.runtimeConfig(workingDir)
	loadRes, err := dashboardagent.BuildModelResolver(ctx, runConfig)
	if err != nil {
		return nil, nil, err
	}
	t := loadRes.Team
	defer t.StopToolSets(context.WithoutCancel(ctx))
	ag, err := t.AgentOrDefault("")
	if err != nil {
		return nil, nil, err
	}

	switcher := &daruntime.ModelSwitcherConfig{
		Models: loadRes.Models, Providers: loadRes.Providers,
		ModelsGateway: runConfig.ModelsGateway, EnvProvider: runConfig.EnvProvider(),
		ProviderRegistry: loadRes.ProviderRegistry, AgentDefaultModels: loadRes.AgentDefaultModels,
	}
	if store, storeErr := runConfig.ModelsDevStore(); storeErr == nil {
		switcher.ModelsStore = store
	}
	rt, err := daruntime.New(ctx, t,
		daruntime.WithCurrentAgent(ag.Name()),
		daruntime.WithWorkingDir(workingDir),
		daruntime.WithModelSwitcherConfig(switcher),
	)
	if err != nil {
		return nil, nil, err
	}
	defer rt.Close()

	if model != "" {
		if err := rt.SetAgentModel(ctx, ag.Name(), model); err != nil {
			return nil, nil, err
		}
	}
	models := modelOptions(rt.AvailableModels(ctx))
	return models, runtimeThinkingLevels(ctx, rt), nil
}

func modelOptions(choices []daruntime.ModelChoice) []protocol.ModelOption {
	out := make([]protocol.ModelOption, 0, len(choices))
	for _, model := range choices {
		out = append(out, protocol.ModelOption{
			Name: model.Name, Ref: model.Ref, Provider: model.Provider, Model: model.Model,
			Family: model.Family, ContextLimit: model.ContextLimit,
			InputCost: model.InputCost, OutputCost: model.OutputCost,
			IsCurrent: model.IsCurrent, IsDefault: model.IsDefault,
			IsCustom: model.IsCustom, IsCatalog: model.IsCatalog,
		})
	}
	return out
}

func runtimeThinkingLevels(ctx context.Context, rt daruntime.Runtime) []string {
	resolver, ok := rt.(interface {
		CurrentAgentThinkingLevels(context.Context) []effort.Level
	})
	if !ok {
		return nil
	}
	levels := resolver.CurrentAgentThinkingLevels(ctx)
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		out = append(out, level.String())
	}
	return out
}

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

func viewFromChecker(c *permissions.Checker, grants []string) protocol.PermissionsView {
	v := protocol.PermissionsView{SessionGrants: grants}
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
			Attributes: summary.Attributes,
			CreatedAt:  summary.CreatedAt.UTC().Format(time.RFC3339),
			Messages:   summary.NumMessages,
			Cost:       summary.Cost,
		})
	}
	return out, nil
}

func (a *Adapter) ReadSession(ctx context.Context, sessionID string) (adapter.StoredSession, error) {
	sess, err := a.store.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return adapter.StoredSession{}, adapter.ErrNotFound
		}
		return adapter.StoredSession{}, err
	}

	agentName, model := storedIdentity(sess)
	reader := &chat{sess: sess, agentName: agentName, model: model}
	items, usage, err := reader.Snapshot(ctx)
	if err != nil {
		return adapter.StoredSession{}, err
	}
	origin := sessionlineage.FromAttributes(sess.AttributesSnapshot())
	return adapter.StoredSession{
		Meta: protocol.StoredSessionMeta{
			SessionID: sess.ID, Title: sess.TitleSnapshot(), WorkingDir: sess.WorkingDir,
			AgentName: agentName, Model: model, CreatedAt: sess.CreatedAt.UTC().Format(time.RFC3339),
			ParentSessionID: origin.ParentSessionID, RootSessionID: origin.RootSessionID,
			OriginKind: origin.Kind, OriginPluginID: origin.PluginID,
		},
		Items: items, Usage: usage, Stats: reader.Stats(ctx),
	}, nil
}

func storedIdentity(sess *session.Session) (agentName, model string) {
	items := sess.MessagesSnapshot()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Message == nil {
			continue
		}
		if agentName == "" {
			agentName = items[i].Message.AgentName
		}
		if model == "" {
			model = items[i].Message.Message.Model
		}
		if agentName != "" && model != "" {
			break
		}
	}
	return agentName, model
}

// OpenChat builds one runtime + session pair. It mirrors the CLI's wiring in
// cmd/root/run.go (createLocalRuntimeAndSession) so toolsets, models, hooks,
// budgets and permissions behave identically.
func (a *Adapter) OpenChat(ctx context.Context, req adapter.OpenRequest) (adapter.Chat, error) {
	a.log.Info("opening agent chat", "chat", req.ChatID, "session", req.ResumeSessionID, "resumed", req.ResumeSessionID != "", "mcp_servers", len(req.MCPServers))
	runConfig := a.runtimeConfig(req.WorkingDir)
	loadRes, err := dashboardagent.Build(ctx, runConfig, req.MCPServers...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", adapter.ErrInvalidAgent, err)
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

	ag, err := t.AgentOrDefault("")
	if err != nil {
		_ = t.StopToolSets(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("%w: %w", adapter.ErrInvalidAgent, err)
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

	steerQueue := newObservableQueue(protocol.DeliverySteer, 5)
	followQueue := newObservableQueue(protocol.DeliveryFollowUp, 20)
	rt, err := daruntime.New(ctx, t,
		daruntime.WithSessionStore(a.store),
		daruntime.WithCurrentAgent(agentName),
		daruntime.WithWorkingDir(req.WorkingDir),
		daruntime.WithModelSwitcherConfig(modelSwitcher),
		daruntime.WithBudget(loadRes.Budget),
		daruntime.WithNamedBudgets(loadRes.Budgets, loadRes.AgentBudgets),
		daruntime.WithRetryOnRateLimit(),
		daruntime.WithSteerQueue(steerQueue),
		daruntime.WithFollowUpQueue(followQueue),
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
			session.WithAttributes(req.SessionAttributes),
			session.WithTitle(placeholderTitle),
		)
		// Like the CLI, the session row is created lazily on the first real
		// message so browsing the dashboard never litters the user's store
		// with empty sessions.
		newSession = true
		if req.PersistImmediately {
			if err := a.store.AddSession(ctx, sess); err != nil {
				_ = rt.Close()
				_ = t.StopToolSets(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("persisting new session: %w", err)
			}
			newSession = false
		}
	}

	// Tool visibility is a dashboard preference backed by docker-agent's
	// session exclusion filter. The field is intentionally non-persistent in
	// docker-agent itself, so restore it on every open/resume.
	sess.ExcludedTools = append([]string(nil), req.DisabledTools...)

	// The dashboard has one safety policy: tools are always auto-approved.
	// Reapply it on resume too, so an older session cannot restore a different
	// policy into this runtime.
	sess.SetSafetyPolicy(session.SafetyPolicyAutonomous)
	if !newSession {
		if err := a.store.UpdateSession(ctx, sess); err != nil {
			a.log.Warn("persisting autonomous safety policy", "error", err)
		}
	}

	c := &chat{
		a:            a,
		rt:           rt,
		team:         t,
		sess:         sess,
		agentName:    agentName,
		workingDir:   req.WorkingDir,
		events:       make(chan protocol.Event, 512),
		unsaved:      newSession,
		pendingTools: map[string]pendingTool{},
		partialTools: map[string]partialTool{},
		pendingElic:  map[string]struct{}{},
		agentsIgnore: agentsIgnore,
		run:          protocol.RunStatus{State: protocol.RunStateIdle},
		steerQueue:   steerQueue,
		followQueue:  followQueue,
	}
	steerQueue.setOnChange(c.queueChanged)
	followQueue.setOnChange(c.queueChanged)
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
	a.log.Info("agent chat ready", "chat", req.ChatID, "session", sess.ID, "agent", agentName, "resumed", !newSession)

	return c, nil
}

// Close closes the shared session store. Individual chats own their runtimes.
func (a *Adapter) Close() error { return a.store.Close() }
