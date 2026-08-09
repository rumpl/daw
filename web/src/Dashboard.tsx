import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { ChatPane } from './components/ChatPane';
import { PluginPage } from './components/PluginPage';
import { PluginCommandPalette, PluginNotifications } from './components/PluginSurfaces';
import { PluginRuntime } from './PluginRuntime';
import { Sidebar } from './components/Sidebar';
import { SessionTabs } from './components/SessionTabs';
import { pluginRoute, sessionRoute } from './routes';
import { clip } from './safety';
import { useDashboard } from './useDashboard';
import { useDashboardEvents } from './useDashboardEvents';
import { usePlugins } from './usePlugins';

interface PluginTabState {
  pluginId: string;
  path: string;
}

interface SplitPaneState {
  id: string;
  sessionId: string;
  workspacePath: string;
}

type PaneLayout =
  | { type: 'leaf'; id: string }
  | { type: 'split'; id: string; direction: 'vertical' | 'horizontal'; size: number; first: PaneLayout; second: PaneLayout };

const PRIMARY_PANE_ID = 'primary';

function splitLeaf(layout: PaneLayout, paneId: string, pane: SplitPaneState, direction: 'vertical' | 'horizontal'): PaneLayout {
  if (layout.type === 'leaf') {
    return layout.id === paneId
      ? { type: 'split', id: `group-${pane.id}`, direction, size: 50, first: layout, second: { type: 'leaf', id: pane.id } }
      : layout;
  }
  return { ...layout, first: splitLeaf(layout.first, paneId, pane, direction), second: splitLeaf(layout.second, paneId, pane, direction) };
}

function removeLeaf(layout: PaneLayout, paneId: string): PaneLayout {
  if (layout.type === 'leaf') return layout;
  if (layout.first.type === 'leaf' && layout.first.id === paneId) return layout.second;
  if (layout.second.type === 'leaf' && layout.second.id === paneId) return layout.first;
  return { ...layout, first: removeLeaf(layout.first, paneId), second: removeLeaf(layout.second, paneId) };
}

function updateSplitSize(layout: PaneLayout, splitId: string, size: number): PaneLayout {
  if (layout.type === 'leaf') return layout;
  if (layout.id === splitId) return { ...layout, size };
  return { ...layout, first: updateSplitSize(layout.first, splitId, size), second: updateSplitSize(layout.second, splitId, size) };
}

export function Dashboard() {
  const params = useParams<{ sessionId: string; pluginId: string; '*': string }>();
  const routeSessionId = params.sessionId;
  const routePluginId = params.pluginId ?? null;
  const routePluginPath = (params['*'] ?? '').replace(/^\/+|\/+$/g, '');
  const location = useLocation();
  const navigate = useNavigate();
  const routeWorkspacePath = useMemo(
    () => new URLSearchParams(location.search).get('workspace'),
    [location.search],
  );
  const [paneLayout, setPaneLayout] = useState<PaneLayout>({ type: 'leaf', id: PRIMARY_PANE_ID });
  const [splitPanes, setSplitPanes] = useState<SplitPaneState[]>([]);
  const nextPaneId = useRef(1);
  const [pluginTabs, setPluginTabs] = useState<PluginTabState[]>([]);
  const dashboardEvents = useDashboardEvents(true);
  const openSession = useCallback(
    (sessionId: string, workspacePath: string) => navigate(sessionRoute(sessionId, workspacePath)),
    [navigate],
  );
  const leaveSession = useCallback(
    () => navigate(routePluginId ? pluginRoute(routePluginId, routePluginPath) : '/'),
    [navigate, routePluginId, routePluginPath],
  );
  const dashboard = useDashboard({
    sessionId: routeSessionId ?? null,
    workspacePath: routeWorkspacePath,
    openSession,
    leaveSession,
  }, dashboardEvents.sessionsRevision);
  const { catalog: pluginCatalog, loadError: pluginLoadError } = usePlugins(
    Boolean(dashboard.boot),
    dashboardEvents.pluginsRevision,
  );
  const activePlugin = pluginCatalog.plugins?.find((plugin) => plugin.id === routePluginId) ?? null;
  const contributionContext = useMemo(() => ({
    workspace: dashboard.workspace,
    chatId: dashboard.chatId,
    session: dashboard.state.meta,
  }), [dashboard.chatId, dashboard.state.meta, dashboard.workspace]);
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const drawerRef = useRef<HTMLDivElement | null>(null);
  const [draggingFiles, setDraggingFiles] = useState(false);
  const dragDepth = useRef(0);

  const openPlugin = useCallback((pluginId: string, path: string) => {
    dashboard.setDrawerOpen(false);
    const openTab = pluginTabs.find((tab) => tab.pluginId === pluginId);
    if (openTab) {
      navigate(pluginRoute(pluginId, openTab.path));
      return;
    }
    setPluginTabs((current) => [...current, { pluginId, path }]);
    navigate(pluginRoute(pluginId, path));
  }, [dashboard.setDrawerOpen, navigate, pluginTabs]);

  const closePlugin = useCallback((pluginId: string) => {
    const closingIndex = pluginTabs.findIndex((tab) => tab.pluginId === pluginId);
    if (closingIndex < 0) return;
    const remaining = pluginTabs.filter((tab) => tab.pluginId !== pluginId);
    setPluginTabs(remaining);
    if (routePluginId !== pluginId) return;

    const adjacentPlugin = remaining[Math.min(closingIndex, remaining.length - 1)];
    if (adjacentPlugin) {
      navigate(pluginRoute(adjacentPlugin.pluginId, adjacentPlugin.path));
      return;
    }
    const adjacentSession = dashboard.liveSessions.find((session) => !splitPanes.some((pane) => pane.sessionId === session.sessionId));
    navigate(adjacentSession ? sessionRoute(adjacentSession.sessionId, adjacentSession.workingDir) : '/');
  }, [dashboard.liveSessions, navigate, pluginTabs, routePluginId, splitPanes]);

  useEffect(() => {
    if (!routePluginId) return;
    setPluginTabs((current) => {
      const index = current.findIndex((tab) => tab.pluginId === routePluginId);
      if (index < 0) return [...current, { pluginId: routePluginId, path: routePluginPath }];
      if (current[index]?.path === routePluginPath) return current;
      return current.map((tab, tabIndex) => tabIndex === index ? { ...tab, path: routePluginPath } : tab);
    });
  }, [routePluginId, routePluginPath]);

  const openSplit = useCallback((
    paneId: string,
    sessionId: string,
    workspacePath: string,
    direction: 'vertical' | 'horizontal',
  ) => {
    const pane: SplitPaneState = { id: `pane-${nextPaneId.current++}`, sessionId, workspacePath };
    setSplitPanes((current) => [...current, pane]);
    setPaneLayout((current) => splitLeaf(current, paneId, pane, direction));

    if (paneId !== PRIMARY_PANE_ID) return;
    const remaining = dashboard.liveSessions.filter((session) => session.sessionId !== sessionId);
    const currentIndex = dashboard.liveSessions.findIndex((session) => session.sessionId === sessionId);
    const replacement = remaining[Math.min(Math.max(currentIndex, 0), remaining.length - 1)];
    if (replacement) navigate(sessionRoute(replacement.sessionId, replacement.workingDir));
    else navigate('/');
  }, [dashboard.liveSessions, navigate]);

  const closeSplit = useCallback((paneId: string) => {
    setSplitPanes((current) => current.filter((pane) => pane.id !== paneId));
    setPaneLayout((current) => removeLeaf(current, paneId));
  }, []);

  const resizeSplit = useCallback((
    event: React.PointerEvent<HTMLDivElement>,
    splitId: string,
    direction: 'vertical' | 'horizontal',
  ) => {
    event.preventDefault();
    const container = event.currentTarget.parentElement;
    if (!container) return;
    const bounds = container.getBoundingClientRect();
    const update = (pointerEvent: PointerEvent) => {
      const position = direction === 'vertical'
        ? pointerEvent.clientX - bounds.left
        : pointerEvent.clientY - bounds.top;
      const total = direction === 'vertical' ? bounds.width : bounds.height;
      setPaneLayout((current) => updateSplitSize(current, splitId, Math.min(80, Math.max(20, (position / total) * 100))));
    };
    const finish = () => {
      document.removeEventListener('pointermove', update);
      document.removeEventListener('pointerup', finish);
      document.body.classList.remove('resizing-split', 'resizing-split-horizontal');
    };
    document.body.classList.add(direction === 'vertical' ? 'resizing-split' : 'resizing-split-horizontal');
    document.addEventListener('pointermove', update);
    document.addEventListener('pointerup', finish);
  }, []);

  const canDropAttachments = Boolean(dashboard.chatId && !routePluginId && !dashboard.uploading);
  const onChatDragEnter = (event: React.DragEvent<HTMLElement>) => {
    if (!canDropAttachments || !event.dataTransfer.types.includes('Files')) return;
    event.preventDefault();
    dragDepth.current += 1;
    setDraggingFiles(true);
  };
  const onChatDragOver = (event: React.DragEvent<HTMLElement>) => {
    if (canDropAttachments && event.dataTransfer.types.includes('Files')) event.preventDefault();
  };
  const onChatDragLeave = (event: React.DragEvent<HTMLElement>) => {
    if (!draggingFiles) return;
    event.preventDefault();
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) setDraggingFiles(false);
  };
  const onChatDrop = (event: React.DragEvent<HTMLElement>) => {
    if (!canDropAttachments) return;
    event.preventDefault();
    dragDepth.current = 0;
    setDraggingFiles(false);
    dashboard.addAttachments(Array.from(event.dataTransfer.files));
  };

  useEffect(() => {
    if (!dashboard.drawerOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        dashboard.setDrawerOpen(false);
        menuButton.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    drawerRef.current?.querySelector<HTMLElement>('button,input,a')?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [dashboard.drawerOpen, dashboard.setDrawerOpen]);

  if (dashboard.bootError) return <main className="fatal"><h1>The dashboard could not start</h1><p>{clip(dashboard.bootError, 300)}</p></main>;
  if (!dashboard.boot) return <main className="fatal"><p>Loading…</p></main>;

  const openPluginTabs = pluginTabs.flatMap((tab) => {
    const plugin = pluginCatalog.plugins?.find((candidate) => candidate.id === tab.pluginId);
    return plugin ? [{ plugin, path: tab.path }] : [];
  });
  const splitSessionIds = new Set(splitPanes.map((pane) => pane.sessionId));
  const mainSessions = dashboard.liveSessions.filter((session) => !splitSessionIds.has(session.sessionId));

  const renderPane = (layout: PaneLayout): ReactNode => {
    if (layout.type === 'split') {
      return (
        <div className={`pane-split ${layout.direction === 'horizontal' ? 'pane-split-horizontal' : ''}`} key={layout.id}>
          <div className="pane-split-child pane-split-sized" style={{ flexBasis: `calc(${layout.size}% - 2.5px)` }}>
            {renderPane(layout.first)}
          </div>
          <div className="split-divider" role="separator" aria-orientation={layout.direction}
            onPointerDown={(event) => resizeSplit(event, layout.id, layout.direction)} />
          <div className="pane-split-child">{renderPane(layout.second)}</div>
        </div>
      );
    }

    if (layout.id !== PRIMARY_PANE_ID) {
      const pane = splitPanes.find((candidate) => candidate.id === layout.id);
      return pane ? (
        <SplitSessionPane key={pane.id} pane={pane} sessionsRevision={dashboardEvents.sessionsRevision}
          onClose={() => closeSplit(pane.id)} />
      ) : null;
    }

    return (
      <section className="main-pane" key={PRIMARY_PANE_ID}>
        <SessionTabs
          sessions={mainSessions} activeSessionId={routePluginId ? null : dashboard.activeSessionId}
          plugins={openPluginTabs} activePluginId={routePluginId} busy={dashboard.busyAction}
          canCreateChat={Boolean(dashboard.workspace)} onNewChat={() => dashboard.newChat()}
          onOpen={dashboard.resumeChat} onClose={dashboard.closeLiveSession}
          onReorder={dashboard.reorderLiveSessions} onOpenPlugin={openPlugin}
          onClosePlugin={closePlugin}
          onSplit={(sessionId, workspacePath, direction) => openSplit(PRIMARY_PANE_ID, sessionId, workspacePath, direction)}
        />
        {routePluginId ? (
          <PluginPage boot={dashboard.boot!} plugin={activePlugin} routePath={routePluginPath}
            workspace={dashboard.workspace} menuButton={menuButton} drawerOpen={dashboard.drawerOpen}
            onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)} />
        ) : <ChatPane dashboard={dashboard} menuButton={menuButton} />}
        {pluginLoadError ? <p className="banner banner-error" role="alert">{clip(pluginLoadError, 300)}</p> : null}
      </section>
    );
  };

  return (
    <div className="app">
      <PluginRuntime boot={dashboard.boot} plugins={pluginCatalog.plugins ?? []} workspace={dashboard.workspace} />
      <PluginNotifications />
      <PluginCommandPalette context={contributionContext} />
      <a className="skip" href="#main">Skip to main content</a>
      {dashboard.drawerOpen ? <div className="scrim" onClick={() => dashboard.setDrawerOpen(false)} role="presentation" /> : null}
      <aside id="sidebar" className={`sidebar ${dashboard.drawerOpen ? 'open' : ''}`} aria-label="Workspace and sessions">
        <Sidebar
          boot={dashboard.boot} workspace={dashboard.workspace} sessions={dashboard.sessions}
          recentWorkspaces={dashboard.recentWorkspaces} plugins={pluginCatalog.plugins ?? []}
          pluginErrors={pluginCatalog.errors ?? []} activePluginId={routePluginId}
          activePluginPath={routePluginPath} workspacePath={dashboard.workspacePath}
          busy={dashboard.busyAction} drawerRef={drawerRef}
          onWorkspacePathChange={dashboard.setWorkspacePath} onOpenWorkspace={dashboard.openWorkspace}
          onNewChat={() => dashboard.newChat()} onResumeChat={dashboard.resumeChat} onOpenPlugin={openPlugin}
        />
      </aside>

      <main id="main" className={`main ${draggingFiles ? 'main-dragging' : ''}`}
        onDragEnter={onChatDragEnter} onDragOver={onChatDragOver} onDragLeave={onChatDragLeave} onDrop={onChatDrop}>
        {draggingFiles ? <div className="chat-drop-hint">Drop files to attach</div> : null}
        <div className="main-panes">
          {renderPane(paneLayout)}
        </div>
      </main>
    </div>
  );
}

interface SplitSessionPaneProps {
  pane: SplitPaneState;
  sessionsRevision: number;
  onClose: () => void;
}

function SplitSessionPane({ pane, sessionsRevision, onClose }: SplitSessionPaneProps) {
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const dashboard = useDashboard({
    sessionId: pane.sessionId,
    workspacePath: pane.workspacePath,
    openSession: () => undefined,
    leaveSession: () => undefined,
  }, sessionsRevision);
  const session = dashboard.liveSessions.find((candidate) => candidate.sessionId === pane.sessionId);

  return (
    <section className="main-pane">
      <SessionTabs
        sessions={session ? [session] : []} activeSessionId={pane.sessionId} busy={dashboard.busyAction}
        canCreateChat={false} onNewChat={() => undefined} onOpen={() => undefined}
        onClose={onClose} onReorder={() => undefined}
      />
      <ChatPane dashboard={dashboard} menuButton={menuButton} showMenu={false} />
    </section>
  );
}
