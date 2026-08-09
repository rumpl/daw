import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import type { Bootstrap, Plugin, PluginError, SessionSummary, Workspace } from '../protocol.gen';
import { clip } from '../safety';
import { PluginSlotView } from './PluginSurfaces';

interface SidebarProps {
  boot: Bootstrap;
  workspace: Workspace | null;
  sessions: SessionSummary[];
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
  onOpenPlugin: (pluginId: string, path: string) => void;
  onOpenPluginSettings?: () => void;
  pluginSettingsActive?: boolean;
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

interface SessionNode {
  session: SessionSummary;
  children: SessionNode[];
}

function sessionForest(sessions: SessionSummary[]): SessionNode[] {
  const nodes = new Map(sessions.map((session) => [session.sessionId, { session, children: [] as SessionNode[] }]));
  const roots: SessionNode[] = [];
  for (const node of nodes.values()) {
    const parent = node.session.parentSessionId ? nodes.get(node.session.parentSessionId) : undefined;
    if (parent && parent !== node) parent.children.push(node);
    else roots.push(node);
  }
  const sort = (items: SessionNode[]) => {
    items.sort((a, b) => b.session.createdAt.localeCompare(a.session.createdAt));
    items.forEach((item) => sort(item.children));
  };
  sort(roots);
  return roots;
}

function groupSessionsByDay(sessions: SessionSummary[]) {
  const groups = new Map<string, SessionNode[]>();
  for (const node of sessionForest(sessions)) {
    const label = sessionDay(node.session.createdAt);
    const group = groups.get(label);
    if (group) group.push(node);
    else groups.set(label, [node]);
  }
  return Array.from(groups, ([label, groupedSessions]) => ({ label, sessions: groupedSessions }));
}

function SessionTreeItem({ node, busy, onResumeChat }: {
  node: SessionNode;
  busy: boolean;
  onResumeChat: (sessionId: string) => void;
}) {
  const session = node.session;
  return (
    <li role="treeitem" aria-expanded={node.children.length ? true : undefined}>
      <button type="button" onClick={() => onResumeChat(session.sessionId)} disabled={busy}>
        <span className="session-title">
          <span>{clip(session.title || 'Untitled', 80)}</span>
          {session.runState === 'running' ? <span className="run-dot run-running" aria-label="Running" /> : null}
        </span>
      </button>
      {node.children.length ? (
        <ul role="group">
          {node.children.map((child) => <SessionTreeItem key={child.session.sessionId} node={child} busy={busy} onResumeChat={onResumeChat} />)}
        </ul>
      ) : null}
    </li>
  );
}

export function Sidebar({
  boot,
  workspace,
  sessions,
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
  onOpenPlugin,
  onOpenPluginSettings,
  pluginSettingsActive,
}: SidebarProps) {
  const contributionContext = { workspace, chatId: null, session: null };
  const [sessionFilter, setSessionFilter] = useState('');
  const [projectPickerOpen, setProjectPickerOpen] = useState(false);
  const [showPathInput, setShowPathInput] = useState(false);
  const projectButtonRef = useRef<HTMLButtonElement | null>(null);
  const pickerRef = useRef<HTMLDivElement | null>(null);
  const projectSwitcherRef = useRef<HTMLDivElement | null>(null);

  const filteredSessions = useMemo(() => {
    const query = sessionFilter.trim().toLowerCase();
    if (!query) return sessions;
    const byID = new Map(sessions.map((session) => [session.sessionId, session]));
    const included = new Set<string>();
    for (const session of sessions) {
      if (!session.title.toLowerCase().includes(query) && !session.sessionId.toLowerCase().includes(query)) continue;
      let current: SessionSummary | undefined = session;
      while (current && !included.has(current.sessionId)) {
        included.add(current.sessionId);
        current = current.parentSessionId ? byID.get(current.parentSessionId) : undefined;
      }
    }
    return sessions.filter((session) => included.has(session.sessionId));
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
      setShowPathInput(false);
      projectButtonRef.current?.focus();
    };
    const onPointerDown = (event: PointerEvent) => {
      if (projectSwitcherRef.current?.contains(event.target as Node)) return;
      setProjectPickerOpen(false);
      setShowPathInput(false);
    };
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('pointerdown', onPointerDown);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('pointerdown', onPointerDown);
    };
  }, [projectPickerOpen]);

  const openProject = (path: string) => {
    setProjectPickerOpen(false);
    setShowPathInput(false);
    onOpenWorkspace(path);
  };

  const projectPicker = projectPickerOpen ? (
    <div className="project-picker" ref={pickerRef} role="dialog" aria-label="Choose a project">
      {projectWorkspaces.length > 0 ? (
        <ul className="project-picker-list" role="menu" aria-label="Projects">
          {projectWorkspaces.map((path) => {
            const current = path === workspace?.path;
            return (
              <li key={path} role="none">
                <button
                  type="button"
                  role="menuitem"
                  title={path}
                  aria-current={current ? 'page' : undefined}
                  onClick={() => openProject(path)}
                  disabled={busy}
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
  ) : null;

  return (
    <div className="sidebar-inner" ref={drawerRef} aria-busy={busy || undefined}>
      <div className="brand">
        docker-agent<span className="brand-sub"> dashboard</span>
      </div>

      <div className="project-switcher-container" ref={projectSwitcherRef}>
        <button
          type="button"
          className="project-switcher"
          ref={projectButtonRef}
          aria-haspopup="menu"
          aria-expanded={projectPickerOpen}
          onClick={() => setProjectPickerOpen((open) => !open)}
        >
          <span>
            <span className="project-name">{workspace ? clip(projectLabel(workspace.path), 60) : 'Choose a project'}</span>
            <span className="project-path">
              {workspace ? clip(workspace.path, 120) : 'Select a working directory'}
            </span>
          </span>
          <span className="project-chevron" aria-hidden="true">⌄</span>
        </button>
        {projectPicker}
      </div>

      <button
        type="button"
        className="block new-chat-button"
        onClick={() => onNewChat()}
        disabled={!workspace || busy}
      >
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

      <div className="sidebar-section-heading">
        <span>Sessions</span>
        <span>{sessions.length}</span>
      </div>

      <section className="sidebar-panel" aria-label="Sessions">
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
                  <ul role="tree">
                    {group.sessions.map((node) => (
                      <SessionTreeItem key={node.session.sessionId} node={node} busy={busy} onResumeChat={onResumeChat} />
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
      </section>

      <PluginSlotView slot="sidebar.footer" context={contributionContext} />
      <button type="button" className="sidebar-settings-button" aria-current={pluginSettingsActive ? 'page' : undefined}
        onClick={() => onOpenPluginSettings?.()}>Settings</button>
      <p className="sidebar-version">docker-agent {clip(boot.agentVersion, 40)}</p>
    </div>
  );
}
