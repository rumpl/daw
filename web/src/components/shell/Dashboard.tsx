import { PanelLeftOpen } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { ChatPane } from '@/components/chat/ChatPane';
import { PluginCommandPalette } from '@/components/plugins/PluginCommandPalette';
import { PluginNotifications } from '@/components/plugins/PluginNotifications';
import { PluginPage } from '@/components/plugins/PluginPage';
import { PluginRuntime } from '@/components/plugins/PluginRuntime';
import { PluginSettingsPage } from '@/components/plugins/PluginSettingsPage';
import { SettingsPage } from '@/components/settings/SettingsPage';
import { ProjectSettings } from '@/components/settings/ProjectSettings';
import { SettingsLayout } from '@/components/settings/SettingsLayout';
import { SettingsHeader } from '@/components/settings/SettingsHeader';
import { Sidebar as DashboardSidebar } from '@/components/sidebar/Sidebar';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Sidebar as ShellSidebar, SidebarInset, SidebarProvider } from '@/components/ui/sidebar';
import { SessionTabs } from '@/components/sessions/SessionTabs';
import { SplitSessionPane } from './SplitSessionPane';
import { newTabSplitShortcut } from './keyboardShortcuts';
import { PRIMARY_PANE_ID, removeLeaf, splitLeaf, updateSplitSize, type PaneLayout, type SplitPaneState } from './paneLayout';
import { pluginRoute, sessionRoute } from '@/routes';
import { clip } from '@/safety';
import { useDashboard } from '@/hooks/useDashboard';
import { useDashboardEvents } from '@/hooks/useDashboardEvents';
import { usePlugins } from '@/hooks/usePlugins';
import { useSessionCompletionChime } from '@/hooks/useSessionCompletionChime';

interface PluginTabState {
  pluginId: string;
  path: string;
}

const SIDEBAR_WIDTH_KEY = 'dawui.sidebar-width';
const SIDEBAR_OPEN_KEY = 'dawui.sidebar-open';
const DEFAULT_SIDEBAR_WIDTH = 268;
const MIN_SIDEBAR_WIDTH = 220;
const MAX_SIDEBAR_WIDTH = 480;

function loadSidebarWidth() {
  try {
    const stored = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
    return Number.isFinite(stored) && stored > 0
      ? Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, stored))
      : DEFAULT_SIDEBAR_WIDTH;
  } catch {
    return DEFAULT_SIDEBAR_WIDTH;
  }
}

function loadSidebarOpen() {
  try {
    return localStorage.getItem(SIDEBAR_OPEN_KEY) !== 'false';
  } catch {
    return true;
  }
}

function decodePathPart(value: string | undefined): string | null {
  if (!value) return null;
  try { return decodeURIComponent(value); } catch { return null; }
}

export function Dashboard() {
  const location = useLocation();
  const sessionMatch = location.pathname.match(/^\/sessions\/([^/]+)\/?$/);
  const pluginMatch = location.pathname.match(/^\/plugins\/([^/]+)(?:\/(.*))?$/);
  const routeSessionId = decodePathPart(sessionMatch?.[1]);
  const routePluginId = decodePathPart(pluginMatch?.[1]);
  const routePluginPath = (pluginMatch?.[2] ?? '').split('/').filter(Boolean)
    .map((part) => decodePathPart(part) ?? '').join('/');
  const settingsActive = location.pathname === '/settings';
  const projectSettingsActive = location.pathname === '/settings/projects';
  const pluginSettingsActive = location.pathname === '/settings/plugins';
  const anySettingsActive = settingsActive || projectSettingsActive || pluginSettingsActive;
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
  }, dashboardEvents.sessionsRevision, dashboardEvents.provisioning, dashboardEvents.clearProvisioning);
  useSessionCompletionChime(
    dashboard.liveSessions,
    routePluginId || anySettingsActive ? null : dashboard.activeSessionId,
  );
  const { catalog: pluginCatalog, loadError: pluginLoadError } = usePlugins(
    Boolean(dashboard.boot),
    dashboardEvents.pluginsRevision,
  );
  const activePlugin = pluginCatalog.plugins?.find((plugin) => plugin.id === routePluginId) ?? null;
  const contributionContext = useMemo(() => ({
    workspace: dashboard.workspace,
    chatId: dashboard.chatId,
    session: dashboard.state.meta,
    sessionId: dashboard.state.meta?.sessionId ?? dashboard.activeSessionId ?? undefined,
  }), [dashboard.activeSessionId, dashboard.chatId, dashboard.state.meta, dashboard.workspace]);
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const drawerRef = useRef<HTMLDivElement | null>(null);
  const settingsReturnRoute = useRef('/');
  const [draggingFiles, setDraggingFiles] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(loadSidebarOpen);
  const [sidebarWidth, setSidebarWidth] = useState(loadSidebarWidth);
  const sidebarAutoCollapsed = useRef(false);
  const dragDepth = useRef(0);

  useEffect(() => {
    if (!anySettingsActive) settingsReturnRoute.current = `${location.pathname}${location.search}`;
  }, [anySettingsActive, location.pathname, location.search]);

  const closeSettings = useCallback(() => navigate(settingsReturnRoute.current), [navigate]);

  const setDesktopSidebarOpen = useCallback((open: boolean) => {
    sidebarAutoCollapsed.current = false;
    setSidebarOpen(open);
    try {
      localStorage.setItem(SIDEBAR_OPEN_KEY, String(open));
    } catch {
      /* Storage is optional. */
    }
  }, []);

  useEffect(() => {
    const narrowWindow = window.matchMedia('(max-width: 820px)');
    const updateForWindowWidth = (event: MediaQueryListEvent | MediaQueryList) => {
      if (event.matches) {
        setSidebarOpen((open) => {
          if (!open) return open;
          sidebarAutoCollapsed.current = true;
          return false;
        });
      } else if (sidebarAutoCollapsed.current) {
        sidebarAutoCollapsed.current = false;
        setSidebarOpen(true);
      }
    };

    updateForWindowWidth(narrowWindow);
    narrowWindow.addEventListener('change', updateForWindowWidth);
    return () => narrowWindow.removeEventListener('change', updateForWindowWidth);
  }, []);

  const resizeSidebar = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (window.matchMedia('(max-width: 820px)').matches) return;
    event.preventDefault();
    const update = (pointerEvent: PointerEvent) => {
      const available = Math.max(MIN_SIDEBAR_WIDTH, window.innerWidth - 320);
      setSidebarWidth(Math.min(MAX_SIDEBAR_WIDTH, available, Math.max(MIN_SIDEBAR_WIDTH, pointerEvent.clientX)));
    };
    const finish = () => {
      document.removeEventListener('pointermove', update);
      document.removeEventListener('pointerup', finish);
      document.removeEventListener('pointercancel', finish);
      document.body.classList.remove('resizing-sidebar');
      setSidebarWidth((width) => {
        try {
          localStorage.setItem(SIDEBAR_WIDTH_KEY, String(width));
        } catch {
          /* Storage is optional. */
        }
        return width;
      });
    };
    document.body.classList.add('resizing-sidebar');
    document.addEventListener('pointermove', update);
    document.addEventListener('pointerup', finish);
    document.addEventListener('pointercancel', finish);
  }, []);

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
    preservePrimarySelection = false,
  ) => {
    const pane: SplitPaneState = { id: `pane-${nextPaneId.current++}`, sessionId, workspacePath };
    setSplitPanes((current) => [...current, pane]);
    setPaneLayout((current) => splitLeaf(current, paneId, pane, direction));

    if (paneId !== PRIMARY_PANE_ID || preservePrimarySelection) return;
    const remaining = dashboard.liveSessions.filter((session) => session.sessionId !== sessionId);
    const currentIndex = dashboard.liveSessions.findIndex((session) => session.sessionId === sessionId);
    const replacement = remaining[Math.min(Math.max(currentIndex, 0), remaining.length - 1)];
    if (replacement) navigate(sessionRoute(replacement.sessionId, replacement.workingDir));
    else navigate('/');
  }, [dashboard.liveSessions, navigate]);

  const openNewTabAndSplit = useCallback((direction: 'vertical' | 'horizontal') => {
    if (dashboard.busyAction || !dashboard.workspace || routePluginId || anySettingsActive) return false;
    const active = dashboard.liveSessions.find((session) => session.sessionId === dashboard.activeSessionId);
    if (!active || active.sessionId.startsWith('draft:')) return false;

    void dashboard.newChat('', true).then((created) => {
      if (!created || created === true) return;
      openSplit(PRIMARY_PANE_ID, created.sessionId, dashboard.workspace!.path, direction, true);
      navigate(sessionRoute(active.sessionId, active.workingDir));
    });
    return true;
  }, [anySettingsActive, dashboard, navigate, openSplit, routePluginId]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.repeat) return;
      const direction = newTabSplitShortcut(event);
      if (!direction || !openNewTabAndSplit(direction)) return;
      event.preventDefault();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [openNewTabAndSplit]);

  const closeSplit = useCallback((paneId: string) => {
    const pane = splitPanes.find((candidate) => candidate.id === paneId);
    setSplitPanes((current) => current.filter((candidate) => candidate.id !== paneId));
    setPaneLayout((current) => removeLeaf(current, paneId));
    if (pane) dashboard.closeSessionTab(pane.sessionId);
  }, [dashboard.closeSessionTab, splitPanes]);

  useEffect(() => {
    if (splitPanes.length === 0 || dashboard.liveSessions.length + pluginTabs.length > 1) return;

    setSplitPanes([]);
    setPaneLayout({ type: 'leaf', id: PRIMARY_PANE_ID });

    const remainingSession = dashboard.liveSessions[0];
    if (remainingSession && !routePluginId && !anySettingsActive && routeSessionId !== remainingSession.sessionId) {
      navigate(sessionRoute(remainingSession.sessionId, remainingSession.workingDir));
    }
  }, [
    anySettingsActive,
    dashboard.liveSessions,
    navigate,
    pluginTabs.length,
    routePluginId,
    routeSessionId,
    splitPanes.length,
  ]);

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

  const canDropAttachments = Boolean(dashboard.workspace && !routePluginId && !dashboard.uploading);
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

  if (dashboard.bootError) return <main className="fatal"><h1>Atelier could not start</h1><p>{clip(dashboard.bootError, 300)}</p></main>;
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
          onClose={() => closeSplit(pane.id)}
          onSplit={(sessionId, workspacePath, direction) => openSplit(pane.id, sessionId, workspacePath, direction)} />
      ) : null;
    }

    return (
      <section className="main-pane" key={PRIMARY_PANE_ID}>
        {settingsActive ? (
          <SettingsPage menuButton={menuButton} drawerOpen={dashboard.drawerOpen}
            onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)} onClose={closeSettings} />
        ) : projectSettingsActive ? (
          <section className="main-pane">
            <SettingsHeader title="Settings · Projects" menuButton={menuButton} drawerOpen={dashboard.drawerOpen}
              onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)} onClose={closeSettings} />
            <div className="settings-page">
              <SettingsLayout>
                <ProjectSettings projects={dashboard.recentWorkspaces} workspacePath={dashboard.workspacePath}
                  onWorkspacePathChange={dashboard.setWorkspacePath} onAddProject={dashboard.addWorkspace}
                  onRemoveProject={dashboard.removeWorkspace} />
              </SettingsLayout>
            </div>
          </section>
        ) : pluginSettingsActive ? (
          <PluginSettingsPage boot={dashboard.boot!} revision={dashboardEvents.pluginsRevision}
            menuButton={menuButton} drawerOpen={dashboard.drawerOpen}
            onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)} onClose={closeSettings} />
        ) : <>
        <SessionTabs
          sessions={mainSessions} activeSessionId={routePluginId ? null : dashboard.activeSessionId}
          plugins={openPluginTabs} activePluginId={routePluginId} busy={dashboard.busyAction}
          canCreateChat={Boolean(dashboard.workspace)} onNewChat={() => dashboard.newChat()}
          onOpen={dashboard.resumeChat} onClose={dashboard.closeSessionTab}
          onReorder={dashboard.reorderLiveSessions} onOpenPlugin={openPlugin}
          onClosePlugin={closePlugin}
          reserveSidebarToggleSpace={!sidebarOpen}
          onSplit={(sessionId, workspacePath, direction) => openSplit(PRIMARY_PANE_ID, sessionId, workspacePath, direction)}
        />
        {routePluginId ? (
          <PluginPage boot={dashboard.boot!} plugin={activePlugin} routePath={routePluginPath}
            workspace={dashboard.workspace} menuButton={menuButton} drawerOpen={dashboard.drawerOpen}
            onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)} />
        ) : <ChatPane dashboard={dashboard} menuButton={menuButton} />}
        {pluginLoadError ? <Alert className="banner banner-error rounded-none border-x-0 border-t-0" variant="destructive"><AlertDescription>{clip(pluginLoadError, 300)}</AlertDescription></Alert> : null}
        </>}
      </section>
    );
  };

  return (
    <SidebarProvider className="app" open={sidebarOpen} onOpenChange={setDesktopSidebarOpen}
      style={{ '--sidebar-w': `${sidebarWidth}px` } as CSSProperties}>
      <PluginRuntime boot={dashboard.boot} plugins={pluginCatalog.plugins ?? []} workspace={dashboard.workspace} />
      <PluginNotifications />
      <PluginCommandPalette context={contributionContext} />
      <a className="skip" href="#main">Skip to main content</a>
      {dashboard.drawerOpen ? <div className="scrim" onClick={() => dashboard.setDrawerOpen(false)} role="presentation" /> : null}
      <ShellSidebar id="sidebar" collapsible="none" className={`sidebar ${sidebarOpen ? '' : 'sidebar-collapsed'} ${dashboard.drawerOpen ? 'open' : ''}`} aria-label="Workspace and sessions">
        <DashboardSidebar
          boot={dashboard.boot} workspace={dashboard.workspace} sessions={dashboard.sessions}
          allSessions={dashboard.allSessions}
          recentWorkspaces={dashboard.recentWorkspaces} projectFolders={dashboard.projectFolders}
          onProjectFoldersChange={dashboard.updateProjectFolders} plugins={pluginCatalog.plugins ?? []}
          pluginErrors={pluginCatalog.errors ?? []} activePluginId={routePluginId}
          activePluginPath={routePluginPath}
          activeSessionId={routePluginId || anySettingsActive ? null : dashboard.activeSessionId}
          busy={dashboard.busyAction} drawerRef={drawerRef}
          onNewChat={dashboard.newChatForWorkspace} onResumeChat={dashboard.resumeChat}
          onStarSession={dashboard.starSession} onOpenPlugin={openPlugin}
          settingsActive={anySettingsActive}
          onOpenSettings={() => { dashboard.setDrawerOpen(false); navigate('/settings'); }}
          onOpenProjectSettings={() => { dashboard.setDrawerOpen(false); navigate('/settings/projects'); }}
          onCollapse={() => setDesktopSidebarOpen(false)}
        />
        <div className="sidebar-resize-handle" role="separator" aria-label="Resize sidebar" aria-orientation="vertical"
          aria-valuemin={MIN_SIDEBAR_WIDTH} aria-valuemax={MAX_SIDEBAR_WIDTH} aria-valuenow={sidebarWidth}
          tabIndex={0} onPointerDown={resizeSidebar} onDoubleClick={() => setDesktopSidebarOpen(false)}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight' && event.key !== 'Home' && event.key !== 'End') return;
            event.preventDefault();
            const width = event.key === 'Home' ? MIN_SIDEBAR_WIDTH : event.key === 'End' ? MAX_SIDEBAR_WIDTH
              : Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, sidebarWidth + (event.key === 'ArrowLeft' ? -10 : 10)));
            setSidebarWidth(width);
            try { localStorage.setItem(SIDEBAR_WIDTH_KEY, String(width)); } catch { /* Storage is optional. */ }
          }} />
      </ShellSidebar>
      {!sidebarOpen ? (
        <Button type="button" size="icon-sm" variant="secondary" className="sidebar-expand-button"
          aria-label="Expand sidebar" onClick={() => setDesktopSidebarOpen(true)}>
          <PanelLeftOpen aria-hidden="true" />
        </Button>
      ) : null}

      <SidebarInset id="main" className={`main ${draggingFiles ? 'main-dragging' : ''}`}
        onDragEnter={onChatDragEnter} onDragOver={onChatDragOver} onDragLeave={onChatDragLeave} onDrop={onChatDrop}>
        {draggingFiles ? <div className="chat-drop-hint">Drop files to attach</div> : null}
        <div className="main-panes">
          {renderPane(paneLayout)}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}
