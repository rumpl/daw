import { useMemo, useState, type RefObject } from 'react';
import type { Bootstrap, SessionSummary, Workspace } from '../protocol.gen';
import { clip } from '../safety';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
  recentWorkspaces: string[];
  workspacePath: string;
  busy: boolean;
  drawerRef: RefObject<HTMLDivElement | null>;
  onWorkspacePathChange: (path: string) => void;
  onOpenWorkspace: (path: string) => void;
  onNewChat: () => void;
  onResumeChat: (sessionId: string) => void;
}

export function Sidebar({
  boot,
  workspace,
  sessions,
  recentWorkspaces,
  workspacePath,
  busy,
  drawerRef,
  onWorkspacePathChange,
  onOpenWorkspace,
  onNewChat,
  onResumeChat,
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
                    {session.live ? ' · live' : ''}
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
