import { useMemo, useState, type RefObject } from 'react';
import type { Bootstrap, SessionSummary, Workspace } from '../protocol.gen';
import { clip } from '../safety';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
  liveSessions: SessionSummary[];
  recentWorkspaces: string[];
  workspacePath: string;
  busy: boolean;
  drawerRef: RefObject<HTMLDivElement | null>;
  onWorkspacePathChange: (path: string) => void;
  onOpenWorkspace: (path: string) => void;
  onNewChat: () => void;
  onResumeChat: (sessionId: string, workspacePath?: string) => void;
  onCloseLiveSession: (sessionId: string, chatId: string) => void;
}

export function Sidebar({
  boot,
  workspace,
  sessions,
  liveSessions,
  recentWorkspaces,
  workspacePath,
  busy,
  drawerRef,
  onWorkspacePathChange,
  onOpenWorkspace,
  onNewChat,
  onResumeChat,
  onCloseLiveSession,
}: SidebarProps) {
  const [sessionFilter, setSessionFilter] = useState('');
  const filteredSessions = useMemo(() => {
    const query = sessionFilter.trim().toLowerCase();
    if (!query) return sessions;
    return sessions.filter(
      (session) =>
        session.title.toLowerCase().includes(query) || session.sessionId.toLowerCase().includes(query),
    );
  }, [sessions, sessionFilter]);
  const projectWorkspaces = recentWorkspaces;

  return (
    <div className="sidebar-inner" ref={drawerRef}>
      <div className="brand">
        docker-agent<span className="brand-sub"> dashboard</span>
      </div>

      <button type="button" className="block" onClick={onNewChat} disabled={!workspace || busy}>
        New chat
      </button>

      <section className="live-sessions">
        <h2>
          Live sessions <span className="section-count">{liveSessions.length}</span>
        </h2>
        {liveSessions.length === 0 ? (
          <p className="hint">No live sessions across your projects.</p>
        ) : (
          <ul className="live-session-list">
            {liveSessions.map((session) => {
              const project =
                session.workingDir.split('/').filter(Boolean).slice(-2).join('/') || session.workingDir;
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
                  <button
                    type="button"
                    className="close-session-button"
                    aria-label={`Close live session ${session.title || 'Untitled'}`}
                    title="Close this live session"
                    onClick={() => onCloseLiveSession(session.sessionId, session.chatId ?? '')}
                    disabled={busy || !session.chatId}
                  >
                    Close
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section>
        <h2>Working directory</h2>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            onOpenWorkspace(workspacePath);
          }}
        >
          <label className="sr-only" htmlFor="ws-path">
            Working directory path
          </label>
          <input
            id="ws-path"
            value={workspacePath}
            onChange={(event) => onWorkspacePathChange(event.target.value)}
            placeholder="/absolute/path/to/project"
            list="ws-hints"
          />
          <datalist id="ws-hints">
            {recentWorkspaces.map((path) => (
              <option key={path} value={path} />
            ))}
          </datalist>
          <button type="submit" disabled={busy}>
            Open
          </button>
        </form>
        {workspace ? (
          <code className="mono-path">{clip(workspace.path, 120)}</code>
        ) : (
          <p className="hint">No workspace selected yet.</p>
        )}

        {projectWorkspaces.length > 0 ? (
          <>
            <h3 className="sub-head">Projects</h3>
            <ul className="recent-list">
              {projectWorkspaces.slice(0, 10).map((path) => {
                const current = path === workspace?.path;
                return (
                  <li key={path}>
                    <button
                      type="button"
                      title={path}
                      aria-current={current ? 'page' : undefined}
                      onClick={() => onOpenWorkspace(path)}
                      disabled={busy || current}
                    >
                      {clip(path.split('/').filter(Boolean).slice(-2).join('/') || path, 40)}
                    </button>
                  </li>
                );
              })}
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
          onChange={(event) => setSessionFilter(event.target.value)}
          placeholder="Search sessions"
        />
        {filteredSessions.length === 0 ? (
          <p className="hint">
            {workspace ? 'No sessions yet for this directory.' : 'Open a workspace to see sessions.'}
          </p>
        ) : (
          <ul className="session-list">
            {filteredSessions.map((session) => (
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
        )}
      </section>

      <p className="sidebar-version">docker-agent {clip(boot.agentVersion, 40)}</p>
    </div>
  );
}
