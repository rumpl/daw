import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import type { Bootstrap, Plugin, PluginError, SessionSummary, Workspace } from '../protocol.gen';
import { clip } from '../safety';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
  liveSessions: SessionSummary[];
  recentWorkspaces: string[];
  plugins: Plugin[];
  pluginErrors: PluginError[];
  activePluginId: string | null;
  activePluginPath: string;
  workspacePath: string;
  busy: boolean;
  drawerRef: RefObject<HTMLDivElement | null>;
  onWorkspacePathChange: (path: string) => void;
  onOpenWorkspace: (path: string) => void;
  onNewChat: () => void;
  onResumeChat: (sessionId: string, workspacePath?: string) => void;
  onCloseLiveSession: (sessionId: string, chatId: string) => void;
  onOpenPlugin: (pluginId: string, path: string) => void;
}

function projectLabel(path: string) {
  return path.split('/').filter(Boolean).slice(-2).join('/') || path;
}

function sessionDay(createdAt: string): string {
  const date = new Date(createdAt);
  if (Number.isNaN(date.getTime())) return 'Unknown date';
  const today = new Date();
  const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const startOfDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const daysAgo = Math.round((startOfToday.getTime() - startOfDate.getTime()) / 86_400_000);
  if (daysAgo === 0) return 'Today';
  if (daysAgo === 1) return 'Yesterday';
  return new Intl.DateTimeFormat(undefined, {
    weekday: 'long',
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() === today.getFullYear() ? undefined : 'numeric',
  }).format(date);
}

function groupSessionsByDay(sessions: SessionSummary[]) {
  const groups = new Map<string, SessionSummary[]>();
  for (const session of sessions) {
    const label = sessionDay(session.createdAt);
    const group = groups.get(label);
    if (group) group.push(session);
    else groups.set(label, [session]);
  }
  return Array.from(groups, ([label, groupedSessions]) => ({ label, sessions: groupedSessions }));
}

export function Sidebar({
  boot,
  workspace,
  sessions,
  liveSessions,
  recentWorkspaces,
  plugins,
  pluginErrors,
  activePluginId,
  activePluginPath,
  workspacePath,
  busy,
  drawerRef,
  onWorkspacePathChange,
  onOpenWorkspace,
  onNewChat,
  onResumeChat,
  onCloseLiveSession,
  onOpenPlugin,
}: SidebarProps) {
  const [activeTab, setActiveTab] = useState<'sessions' | 'live'>('sessions');
  const [sessionFilter, setSessionFilter] = useState('');
  const [projectPickerOpen, setProjectPickerOpen] = useState(false);
  const [showPathInput, setShowPathInput] = useState(false);
  const projectButtonRef = useRef<HTMLButtonElement | null>(null);
  const pickerRef = useRef<HTMLDivElement | null>(null);

  const filteredSessions = useMemo(() => {
    const query = sessionFilter.trim().toLowerCase();
    if (!query) return sessions;
    return sessions.filter(
      (session) =>
        session.title.toLowerCase().includes(query) || session.sessionId.toLowerCase().includes(query),
    );
  }, [sessions, sessionFilter]);

  const groupedSessions = useMemo(() => groupSessionsByDay(filteredSessions), [filteredSessions]);

  const projectWorkspaces = useMemo(
    () => Array.from(new Set([workspace?.path, ...recentWorkspaces].filter((path): path is string => Boolean(path)))),
    [recentWorkspaces, workspace?.path],
  );
  const pathInputVisible = showPathInput || projectWorkspaces.length === 0;

  useEffect(() => {
    if (!projectPickerOpen) return;
    pickerRef.current?.querySelector<HTMLElement>('button:not([disabled]),input')?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopImmediatePropagation();
      setProjectPickerOpen(false);
      projectButtonRef.current?.focus();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [projectPickerOpen]);

  const closeProjectPicker = () => {
    setProjectPickerOpen(false);
    setShowPathInput(false);
    projectButtonRef.current?.focus();
  };

  const openProject = (path: string) => {
    setProjectPickerOpen(false);
    setShowPathInput(false);
    onOpenWorkspace(path);
  };

  const projectPicker = projectPickerOpen
    ? createPortal(
        <div className="dialog-scrim project-picker-scrim" role="presentation" onMouseDown={closeProjectPicker}>
          <div
            className="dialog project-picker"
            role="dialog"
            aria-modal="true"
            aria-labelledby="project-picker-title"
            ref={pickerRef}
            onMouseDown={(event) => event.stopPropagation()}
          >
            <div className="project-picker-head">
              <h2 id="project-picker-title">Choose a project</h2>
              <button type="button" aria-label="Close project picker" onClick={closeProjectPicker}>
                Close
              </button>
            </div>

            {projectWorkspaces.length > 0 ? (
              <ul className="project-picker-list">
                {projectWorkspaces.map((path) => {
                  const current = path === workspace?.path;
                  return (
                    <li key={path}>
                      <button
                        type="button"
                        title={path}
                        aria-current={current ? 'page' : undefined}
                        onClick={() => openProject(path)}
                        disabled={busy || current}
                      >
                        <span className="project-name">{clip(projectLabel(path), 60)}</span>
                        <span className="project-path">{clip(path, 160)}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="hint">No recent projects.</p>
            )}

            {!pathInputVisible ? (
              <button type="button" className="open-directory-button" onClick={() => setShowPathInput(true)}>
                Open another directory…
              </button>
            ) : (
              <form
                className="project-path-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  openProject(workspacePath);
                }}
              >
                <label htmlFor="ws-path">Working directory path</label>
                <div className="project-path-row">
                  <input
                    id="ws-path"
                    value={workspacePath}
                    onChange={(event) => onWorkspacePathChange(event.target.value)}
                    placeholder="/absolute/path/to/project"
                    list="ws-hints"
                    autoFocus={projectWorkspaces.length === 0}
                  />
                  <datalist id="ws-hints">
                    {recentWorkspaces.map((path) => (
                      <option key={path} value={path} />
                    ))}
                  </datalist>
                  <button type="submit" disabled={busy || !workspacePath.trim()}>
                    Open
                  </button>
                </div>
              </form>
            )}

          </div>
        </div>,
        document.body,
      )
    : null;

  return (
    <div className="sidebar-inner" ref={drawerRef}>
      <div className="brand">
        docker-agent<span className="brand-sub"> dashboard</span>
      </div>

      <button
        type="button"
        className="project-switcher"
        ref={projectButtonRef}
        aria-haspopup="dialog"
        onClick={() => setProjectPickerOpen(true)}
      >
        <span>
          <span className="project-name">{workspace ? clip(projectLabel(workspace.path), 60) : 'Choose a project'}</span>
          <span className="project-path">
            {workspace ? clip(workspace.path, 120) : 'Select a working directory'}
          </span>
        </span>
        <span className="project-chevron" aria-hidden="true">›</span>
      </button>

      <button type="button" className="block new-chat-button" onClick={onNewChat} disabled={!workspace || busy}>
        New chat
      </button>

      {plugins.some((plugin) => plugin.pages?.some((page) => page.sidebar)) ? (
        <nav className="plugin-navigation" aria-label="Plugins">
          <p className="sidebar-heading">Plugins</p>
          <ul>
            {plugins.flatMap((plugin) =>
              (plugin.pages ?? []).filter((page) => page.sidebar).map((page) => (
                <li key={`${plugin.id}:${page.id}`}>
                  <button
                    type="button"
                    aria-current={activePluginId === plugin.id && activePluginPath === page.path ? 'page' : undefined}
                    title={plugin.description || plugin.name}
                    onClick={() => onOpenPlugin(plugin.id, page.path)}
                  >
                    {clip(page.label, 60)}
                  </button>
                </li>
              )),
            )}
          </ul>
        </nav>
      ) : null}
      {pluginErrors.length > 0 ? (
        <p className="plugin-discovery-error" title={pluginErrors.map((error) => `${error.pluginId}: ${error.message}`).join('\n')}>
          {pluginErrors.length} invalid plugin{pluginErrors.length === 1 ? '' : 's'}
        </p>
      ) : null}

      <div className="sidebar-tabs" role="tablist" aria-label="Chat navigation">
        <button
          type="button"
          role="tab"
          id="sessions-tab"
          aria-selected={activeTab === 'sessions'}
          aria-controls="sessions-panel"
          onClick={() => setActiveTab('sessions')}
        >
          Sessions <span>{sessions.length}</span>
        </button>
        <button
          type="button"
          role="tab"
          id="live-tab"
          aria-selected={activeTab === 'live'}
          aria-controls="live-panel"
          onClick={() => setActiveTab('live')}
        >
          Live sessions <span>{liveSessions.length}</span>
        </button>
      </div>

      {activeTab === 'sessions' ? (
        <section className="sidebar-panel" role="tabpanel" id="sessions-panel" aria-labelledby="sessions-tab">
          <label className="sr-only" htmlFor="session-search">Search sessions</label>
          <input
            id="session-search"
            value={sessionFilter}
            onChange={(event) => setSessionFilter(event.target.value)}
            placeholder="Search sessions"
          />
          {filteredSessions.length === 0 ? (
            <p className="hint">
              {workspace ? 'No sessions yet for this project.' : 'Choose a project to see sessions.'}
            </p>
          ) : (
            <div className="session-list">
              {groupedSessions.map((group, index) => (
                <section
                  className={`session-day${group.label === 'Today' ? ' session-day-current' : ''}`}
                  key={group.label}
                  aria-labelledby={`session-day-${index}`}
                >
                  <h3 id={`session-day-${index}`}>{group.label}</h3>
                  <ul>
                    {group.sessions.map((session) => (
                      <li key={session.sessionId}>
                        <button type="button" onClick={() => onResumeChat(session.sessionId)} disabled={busy}>
                          <span className="session-title">{clip(session.title || 'Untitled', 80)}</span>
                          <span className="session-meta">
                            {session.live ? <span className="live-dot" aria-hidden="true" /> : null}
                            {session.messages} message{session.messages === 1 ? '' : 's'}
                            {session.live
                              ? ` · ${session.runState === 'running' ? 'Running' : session.runState === 'stopping' ? 'Stopping' : 'Idle'}`
                              : ''}
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
        </section>
      ) : (
        <section className="sidebar-panel" role="tabpanel" id="live-panel" aria-labelledby="live-tab">
          {liveSessions.length === 0 ? (
            <p className="hint">No live sessions across your projects.</p>
          ) : (
            <ul className="session-list live-session-list">
              {liveSessions.map((session) => {
                const project = projectLabel(session.workingDir);
                const status = session.runState === 'running' ? 'Running' : session.runState === 'stopping' ? 'Stopping' : 'Idle';
                return (
                  <li key={session.sessionId} className="live-session-row">
                    <button
                      type="button"
                      className="live-session-open"
                      aria-label={`Open live session ${session.title || 'Untitled'} in ${session.workingDir}`}
                      onClick={() => onResumeChat(session.sessionId, session.workingDir)}
                      disabled={busy}
                    >
                      <span className="session-title">{clip(session.title || 'Untitled', 80)}</span>
                      <span className="session-project" title={session.workingDir}>
                        {clip(project, 48)} ·{' '}
                        <span className={`run-state run-${session.runState ?? 'idle'}`}>
                          <span className="run-dot" aria-hidden="true" />
                          {status}
                        </span>
                      </span>
                    </button>
                    <details className="live-session-menu">
                      <summary aria-label={`Actions for live session ${session.title || 'Untitled'}`}>•••</summary>
                      <button
                        type="button"
                        className="close-session-button"
                        aria-label={`Close live session ${session.title || 'Untitled'}`}
                        onClick={(event) => {
                          onCloseLiveSession(session.sessionId, session.chatId ?? '');
                          event.currentTarget.closest('details')?.removeAttribute('open');
                        }}
                        disabled={busy || !session.chatId}
                      >
                        Close session
                      </button>
                    </details>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      )}

      <p className="sidebar-version">docker-agent {clip(boot.agentVersion, 40)}</p>
      {projectPicker}
    </div>
  );
}
