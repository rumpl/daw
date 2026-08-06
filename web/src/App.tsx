import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError } from './api';
import type {
  Bootstrap,
  CommandInfo,
  ModelOption,
  Posture,
  SessionSummary,
  UpdateConfigRequest,
  Workspace,
} from './protocol.gen';
import { useChat } from './useChat';
import { Conversation } from './components/Conversation';
import { Composer, type SendMode } from './components/Composer';
import { ElicitationDialog, ToolConfirmDialog } from './components/Dialogs';
import { ModelPicker } from './components/ModelPicker';
import { clip, formatCost, formatTokens } from './safety';

const LS_PREFS = 'dawui.prefs.v1';
const LS_DRAFT = 'dawui.draft.';

interface Prefs {
  /** Most-recent-first list of workspace paths that opened successfully. */
  recentWorkspaces: string[];
  deliveryMode: SendMode;
}

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(LS_PREFS);
    if (!raw) return { recentWorkspaces: [], deliveryMode: 'normal' };
    const parsed = JSON.parse(raw) as Partial<Prefs>;
    return {
      recentWorkspaces: (parsed.recentWorkspaces ?? []).filter((p) => typeof p === 'string').slice(0, 8),
      deliveryMode: parsed.deliveryMode ?? 'normal',
    };
  } catch {
    return { recentWorkspaces: [], deliveryMode: 'normal' };
  }
}

function savePrefs(p: Prefs) {
  try {
    localStorage.setItem(LS_PREFS, JSON.stringify(p));
  } catch {
    /* storage disabled: preferences are optional */
  }
}

export function App() {
  const [boot, setBoot] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState<string>('');
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs);

  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [sessionFilter, setSessionFilter] = useState('');
  const [chatId, setChatId] = useState<string | null>(null);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  const [error, setError] = useState<string>('');
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [controlsOpen, setControlsOpen] = useState(false);
  const [workspacePath, setWorkspacePath] = useState('');
  const [draft, setDraft] = useState('');
  const [busyAction, setBusyAction] = useState(false);

  const { state, connection, resnapshot } = useChat(chatId);
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const settingsButton = useRef<HTMLButtonElement | null>(null);
  const drawerRef = useRef<HTMLDivElement | null>(null);

  // --- bootstrap -----------------------------------------------------------
  useEffect(() => {
    api
      .bootstrap()
      .then((b) => {
        setBoot(b);
        setWorkspacePath(prefs.recentWorkspaces[0] ?? b.workspaceHints?.[0]?.path ?? '');
      })
      .catch((e: unknown) => setBootError(e instanceof Error ? e.message : 'failed to reach the server'));
  }, []);

  // --- drafts --------------------------------------------------------------
  useEffect(() => {
    if (!chatId) return;
    setDraft(localStorage.getItem(LS_DRAFT + chatId) ?? '');
  }, [chatId]);

  useEffect(() => {
    if (!chatId) return;
    try {
      if (draft) localStorage.setItem(LS_DRAFT + chatId, draft);
      else localStorage.removeItem(LS_DRAFT + chatId);
    } catch {
      /* ignore */
    }
  }, [chatId, draft]);

  // --- drawer accessibility -----------------------------------------------
  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setDrawerOpen(false);
        menuButton.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    drawerRef.current?.querySelector<HTMLElement>('button,input,a')?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [drawerOpen]);

  useEffect(() => {
    if (!controlsOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setControlsOpen(false);
        settingsButton.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [controlsOpen]);

  const guard = useCallback(async (fn: () => Promise<void>) => {
    setBusyAction(true);
    setError('');
    try {
      await fn();
    } catch (e: unknown) {
      setError(e instanceof ApiError ? e.message : 'the request failed');
    } finally {
      setBusyAction(false);
    }
  }, []);

  const refreshSessions = useCallback(async (ws: Workspace) => {
    const list = await api.sessions(ws.workspaceId);
    setSessions(list);
  }, []);

  const rememberWorkspace = useCallback((path: string) => {
    setPrefs((p) => {
      const next = { ...p, recentWorkspaces: [path, ...p.recentWorkspaces.filter((x) => x !== path)].slice(0, 8) };
      savePrefs(next);
      return next;
    });
  }, []);

  const forgetWorkspace = useCallback((path: string) => {
    setPrefs((p) => {
      const next = { ...p, recentWorkspaces: p.recentWorkspaces.filter((x) => x !== path) };
      savePrefs(next);
      return next;
    });
  }, []);

  const applyWorkspace = useCallback(
    async (path: string) => {
      const ws = await api.openWorkspace(path);
      setWorkspace(ws);
      setWorkspacePath(ws.path);
      setChatId(null);
      await refreshSessions(ws);
      rememberWorkspace(ws.path);
    },
    [refreshSessions, rememberWorkspace],
  );

  const openWorkspace = (path: string) => guard(() => applyWorkspace(path));

  // Reopen the workspace last used in this browser. A path that has since
  // moved, been deleted, or fallen outside the allowed roots is dropped from
  // the list silently rather than greeting the user with an error.
  const autoOpened = useRef(false);
  useEffect(() => {
    if (!boot || autoOpened.current) return;
    autoOpened.current = true;
    const last = prefs.recentWorkspaces[0];
    if (!last) return;
    applyWorkspace(last).catch(() => forgetWorkspace(last));
  }, [boot, prefs.recentWorkspaces, applyWorkspace, forgetWorkspace]);


  const loadChatExtras = useCallback(async (id: string) => {
    const [m, c] = await Promise.all([api.models(id), api.commands(id)]);
    setModels(m);
    setCommands(c);
  }, []);

  const newChat = () =>
    guard(async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      // No agent chosen? The server uses docker-agent's built-in coder.
      const ref = await api.createChat(workspace.workspaceId);
      setChatId(ref.chatId);
      setDrawerOpen(false);
      await loadChatExtras(ref.chatId);
      await refreshSessions(workspace);
    });

  const resumeChat = (sessionId: string) =>
    guard(async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      const ref = await api.resumeChat(workspace.workspaceId, '', sessionId);
      setChatId(ref.chatId);
      setDrawerOpen(false);
      await loadChatExtras(ref.chatId);
      await refreshSessions(workspace);
    });

  const send = (text: string, mode: SendMode) =>
    guard(async () => {
      if (!chatId) return;
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      await api.send(chatId, text, mode, key);
      setDraft('');
    });

  const busy = state.run.state !== 'idle';

  const patchConfig = (patch: { model?: string; thinkingLevel?: string; posture?: Posture }) =>
    guard(async () => {
      if (!chatId) return;
      // Selecting autonomous in the dropdown IS the explicit action; the
      // server still requires the flag so a stray request cannot widen it.
      const body: UpdateConfigRequest = { confirmAutoApprove: patch.posture === 'autonomous' };
      if (patch.model !== undefined) body.model = patch.model;
      if (patch.thinkingLevel !== undefined) body.thinkingLevel = patch.thinkingLevel;
      if (patch.posture !== undefined) body.posture = patch.posture;
      await api.updateConfig(chatId, body);
      await loadChatExtras(chatId);
      setControlsOpen(false);
    });

  const connectionLabel =
    connection === 'connected'
      ? 'Connected'
      : connection === 'reconnecting'
        ? 'Reconnecting…'
        : connection === 'connecting'
          ? 'Connecting…'
          : 'Disconnected';

  const filteredSessions = useMemo(() => {
    const q = sessionFilter.trim().toLowerCase();
    if (!q) return sessions;
    return sessions.filter((s) => s.title.toLowerCase().includes(q) || s.sessionId.toLowerCase().includes(q));
  }, [sessions, sessionFilter]);

  if (bootError) {
    return (
      <main className="fatal">
        <h1>The dashboard could not start</h1>
        <p>{clip(bootError, 300)}</p>
        <p>Check that the server is running, then reload this page.</p>
      </main>
    );
  }
  if (!boot) {
    return (
      <main className="fatal">
        <p>Loading…</p>
      </main>
    );
  }

  const sidebar = (
    <div className="sidebar-inner" ref={drawerRef}>
      <div className="brand">
        docker-agent<span className="brand-sub"> dashboard</span>
      </div>

      <button type="button" className="block" onClick={newChat} disabled={!workspace || busyAction}>
        New chat
      </button>

      <section>
        <h2>Working directory</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void openWorkspace(workspacePath);
          }}
        >
          <label className="sr-only" htmlFor="ws-path">
            Working directory path
          </label>
          <input
            id="ws-path"
            value={workspacePath}
            onChange={(e) => setWorkspacePath(e.target.value)}
            placeholder="/absolute/path/to/project"
            list="ws-hints"
          />
          <datalist id="ws-hints">
            {prefs.recentWorkspaces.map((p) => (
              <option key={p} value={p} />
            ))}
            {(boot.workspaceHints ?? []).map((h) => (
              <option key={h.path} value={h.path} />
            ))}
          </datalist>
          <button type="submit" disabled={busyAction}>
            Open
          </button>
        </form>
        {workspace ? (
          <code className="mono-path">{clip(workspace.path, 120)}</code>
        ) : (
          <p className="hint">No workspace selected yet.</p>
        )}

        {prefs.recentWorkspaces.filter((p) => p !== workspace?.path).length > 0 ? (
          <>
            <h3 className="sub-head">Recent</h3>
            <ul className="recent-list">
              {prefs.recentWorkspaces
                .filter((p) => p !== workspace?.path)
                .slice(0, 5)
                .map((p) => (
                  <li key={p}>
                    <button type="button" title={p} onClick={() => void openWorkspace(p)} disabled={busyAction}>
                      {clip(p.split('/').filter(Boolean).slice(-2).join('/') || p, 40)}
                    </button>
                  </li>
                ))}
            </ul>
          </>
        ) : null}

        <p className="hint small">Allowed roots: {clip((boot.workspaceRoots ?? []).join(', '), 200)}</p>
      </section>

      <section className="sessions">
        <h2>Sessions</h2>
        <label className="sr-only" htmlFor="session-search">
          Search sessions
        </label>
        <input
          id="session-search"
          value={sessionFilter}
          onChange={(e) => setSessionFilter(e.target.value)}
          placeholder="Search sessions"
        />
        {filteredSessions.length === 0 ? (
          <p className="hint">{workspace ? 'No sessions yet for this directory.' : 'Open a workspace to see sessions.'}</p>
        ) : (
          <ul className="session-list">
            {filteredSessions.map((s) => (
              <li key={s.sessionId}>
                <button type="button" onClick={() => void resumeChat(s.sessionId)} disabled={busyAction}>
                  <span className="session-title">{clip(s.title || 'Untitled', 80)}</span>
                  <span className="session-meta">
                    {s.live ? <span className="live-dot" aria-hidden="true" /> : null}
                    {s.messages} message{s.messages === 1 ? '' : 's'}
                    {s.live ? ' · live' : ''}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="about">
        <h2>About</h2>
        <p className="hint small">
          docker-agent {clip(boot.agentVersion, 40)}
          {boot.agentCommit ? ` · ${clip(boot.agentCommit, 7)}` : ''}
        </p>
        <p className="hint small">data: {clip(boot.dataDir, 120)}</p>
        <p className="hint small">
          New chats start in {clip(boot.defaultPosture, 20)}
          {boot.defaultPosture === 'autonomous' ? ' (auto-approve)' : ''}
        </p>
        <p className="hint small">
          No sandbox — tools run on this host as you. Use <code>docker agent run --sandbox</code> for isolation.
        </p>
      </section>
    </div>
  );

  return (
    <div className="app">
      <a className="skip" href="#main">
        Skip to conversation
      </a>

      {drawerOpen ? <div className="scrim" onClick={() => setDrawerOpen(false)} role="presentation" /> : null}

      <aside id="sidebar" className={`sidebar ${drawerOpen ? 'open' : ''}`} aria-label="Workspace and sessions">
        {sidebar}
      </aside>

      <main id="main" className="main">
        <header className="topbar">
          <button
            type="button"
            className="menu-button"
            ref={menuButton}
            aria-expanded={drawerOpen}
            aria-controls="sidebar"
            onClick={() => setDrawerOpen((v) => !v)}
          >
            Menu
          </button>

          <div className="topbar-title">
            <h1>{clip(state.meta?.title || 'docker-agent', 80)}</h1>
            {/* The stream only exists once a chat is open; before that there
                is nothing to report. */}
            {chatId ? (
              <span className={`conn conn-${connection}`} role="status" aria-label={connectionLabel}>
                <span className="conn-dot" aria-hidden="true" />
                <span className="conn-text">{connectionLabel}</span>
              </span>
            ) : null}
          </div>

          {chatId && state.meta ? (
            <>
              <button
                type="button"
                className="settings-toggle"
                ref={settingsButton}
                aria-expanded={controlsOpen}
                aria-controls="chat-controls"
                onClick={() => setControlsOpen((v) => !v)}
              >
                Settings
              </button>

              {controlsOpen ? (
                <div className="controls-scrim" role="presentation" onClick={() => setControlsOpen(false)} />
              ) : null}

              <div id="chat-controls" className={`controls ${controlsOpen ? 'open' : ''}`}>
                <ModelPicker
                  models={models}
                  current={state.meta.model}
                  disabled={busy || busyAction || models.length === 0}
                  onSelect={(ref) => void patchConfig({ model: ref })}
                />

                <label>
                  <span className="sr-only">Thinking budget</span>
                  <select
                    value={state.meta.thinkingLevel}
                    disabled={busy || busyAction || (state.meta.thinkingLevels ?? []).length === 0}
                    onChange={(e) => void patchConfig({ thinkingLevel: e.target.value })}
                  >
                    {(state.meta.thinkingLevels ?? []).length === 0 ? <option value="">thinking: n/a</option> : null}
                    {state.meta.thinkingLevel && !(state.meta.thinkingLevels ?? []).includes(state.meta.thinkingLevel) ? (
                      <option value={state.meta.thinkingLevel}>thinking: {clip(state.meta.thinkingLevel, 20)}</option>
                    ) : null}
                    {(state.meta.thinkingLevels ?? []).map((l) => (
                      <option key={l} value={l}>
                        thinking: {l}
                      </option>
                    ))}
                  </select>
                </label>

                <label>
                  <span className="sr-only">Tool approval mode</span>
                  <select
                    value={state.meta.permissions.posture}
                    disabled={busy || busyAction}
                    onChange={(e) => void patchConfig({ posture: e.target.value as Posture })}
                  >
                    <option value="autonomous">tools: auto-approve</option>
                    <option value="balanced">tools: safe only</option>
                    <option value="strict">tools: ask always</option>
                  </select>
                </label>

                <div className="control-row">
                  <button
                    type="button"
                    onClick={() => void guard(() => api.compact(chatId).then(() => undefined))}
                    disabled={busy || busyAction}
                  >
                    Compact
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      const title = window.prompt('New title', state.meta?.title ?? '');
                      if (title) void guard(() => api.retitle(chatId, title).then(() => undefined));
                    }}
                    disabled={busyAction}
                  >
                    Rename
                  </button>
                </div>

                <span className="usage">
                  {formatTokens(state.usage.inputTokens + state.usage.outputTokens)} tokens · {formatCost(state.usage.cost)}
                </span>
              </div>
            </>
          ) : null}
        </header>

        {error ? (
          <p className="banner banner-error" role="alert">
            {clip(error, 300)}
          </p>
        ) : null}

        {connection === 'disconnected' && chatId ? (
          <p className="banner banner-warn">
            Disconnected from the event stream.
            <button type="button" onClick={() => void resnapshot()}>
              Retry now
            </button>
          </p>
        ) : null}

        {state.closed ? <p className="banner banner-warn">This chat was closed ({clip(state.closedReason, 80)}).</p> : null}

        <Conversation
          items={state.items}
          empty={
            !workspace ? (
              <>
                <h2>Pick a working directory</h2>
                <p>Open a folder in the sidebar and start a chat.</p>
              </>
            ) : !chatId ? (
              <>
                <h2>Ready when you are</h2>
                <p>
                  Start a new chat or resume a session from the sidebar. Working in{' '}
                  <code>{clip(workspace.label, 40)}</code>.
                </p>
              </>
            ) : (
              <>
                <h2>Say something</h2>
                <p>Ask for a change, a review, or an explanation. Tools run in {clip(workspace.label, 40)}.</p>
              </>
            )
          }
        />

        {chatId ? (
          <Composer
            draft={draft}
            onDraftChange={setDraft}
            run={state.run}
            disabled={busyAction || state.closed}
            commands={commands}
            onSend={send}
            onStop={() => void guard(() => api.abort(chatId).then(() => undefined))}
          />
        ) : null}
      </main>

      {state.confirmations[0] ? (
        <ToolConfirmDialog
          request={state.confirmations[0]}
          onDecide={(decision, reason) =>
            void guard(async () => {
              if (!chatId || !state.confirmations[0]) return;
              await api.confirmTool(chatId, {
                toolCallId: state.confirmations[0].toolCallId,
                decision,
                reason,
              });
            })
          }
        />
      ) : state.elicitations[0] ? (
        <ElicitationDialog
          request={state.elicitations[0]}
          onAnswer={(action, content) =>
            void guard(async () => {
              if (!chatId || !state.elicitations[0]) return;
              await api.answerElicitation(chatId, {
                elicitationId: state.elicitations[0].elicitationId,
                action,
                content,
              });
            })
          }
        />
      ) : null}
    </div>
  );
}
