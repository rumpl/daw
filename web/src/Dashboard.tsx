import { useCallback, useEffect, useMemo, useRef } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { ChatHeader } from './components/ChatHeader';
import { Composer } from './components/Composer';
import { Conversation } from './components/Conversation';
import { PendingDialogs } from './components/PendingDialogs';
import { Sidebar } from './components/Sidebar';
import { sessionRoute } from './routes';
import { clip } from './safety';
import { useDashboard } from './useDashboard';

export function Dashboard() {
  const { sessionId: routeSessionId } = useParams<{ sessionId: string }>();
  const location = useLocation();
  const navigate = useNavigate();
  const routeWorkspacePath = useMemo(
    () => new URLSearchParams(location.search).get('workspace'),
    [location.search],
  );
  const openSession = useCallback(
    (sessionId: string, workspacePath: string) => navigate(sessionRoute(sessionId, workspacePath)),
    [navigate],
  );
  const leaveSession = useCallback(() => navigate('/'), [navigate]);
  const dashboard = useDashboard({
    sessionId: routeSessionId ?? null,
    workspacePath: routeWorkspacePath,
    openSession,
    leaveSession,
  });
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const drawerRef = useRef<HTMLDivElement | null>(null);

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

  if (dashboard.bootError) {
    return (
      <main className="fatal">
        <h1>The dashboard could not start</h1>
        <p>{clip(dashboard.bootError, 300)}</p>
        <p>Check that the server is running, then reload this page.</p>
      </main>
    );
  }
  if (!dashboard.boot) {
    return (
      <main className="fatal">
        <p>Loading…</p>
      </main>
    );
  }

  return (
    <div className="app">
      <a className="skip" href="#main">
        Skip to conversation
      </a>

      {dashboard.drawerOpen ? (
        <div className="scrim" onClick={() => dashboard.setDrawerOpen(false)} role="presentation" />
      ) : null}

      <aside
        id="sidebar"
        className={`sidebar ${dashboard.drawerOpen ? 'open' : ''}`}
        aria-label="Workspace and sessions"
      >
        <Sidebar
          boot={dashboard.boot}
          workspace={dashboard.workspace}
          sessions={dashboard.sessions}
          recentWorkspaces={dashboard.recentWorkspaces}
          workspacePath={dashboard.workspacePath}
          busy={dashboard.busyAction}
          drawerRef={drawerRef}
          onWorkspacePathChange={dashboard.setWorkspacePath}
          onOpenWorkspace={dashboard.openWorkspace}
          onNewChat={dashboard.newChat}
          onResumeChat={dashboard.resumeChat}
        />
      </aside>

      <main id="main" className="main">
        <ChatHeader
          hasChat={Boolean(dashboard.chatId)}
          state={dashboard.state}
          connection={dashboard.connection}
          models={dashboard.models}
          busyAction={dashboard.busyAction}
          menuButton={menuButton}
          drawerOpen={dashboard.drawerOpen}
          onToggleDrawer={() => dashboard.setDrawerOpen((open) => !open)}
          onPatchConfig={dashboard.patchConfig}
          onCompact={dashboard.compact}
          onRename={dashboard.rename}
        />

        {dashboard.error ? (
          <p className="banner banner-error" role="alert">
            {clip(dashboard.error, 300)}
          </p>
        ) : null}

        {dashboard.connection === 'disconnected' && dashboard.chatId ? (
          <p className="banner banner-warn">
            Disconnected from the event stream.
            <button type="button" onClick={() => void dashboard.resnapshot()}>
              Retry now
            </button>
          </p>
        ) : null}

        {dashboard.state.closed ? (
          <p className="banner banner-warn">
            This chat was closed ({clip(dashboard.state.closedReason, 80)}).
          </p>
        ) : null}

        <Conversation
          items={dashboard.state.items}
          empty={
            !dashboard.workspace ? (
              <>
                <h2>Pick a working directory</h2>
                <p>Open a folder in the sidebar and start a chat.</p>
              </>
            ) : !dashboard.chatId ? (
              <>
                <h2>Ready when you are</h2>
                <p>
                  Start a new chat or resume a session from the sidebar. Working in{' '}
                  <code>{clip(dashboard.workspace.label, 40)}</code>.
                </p>
              </>
            ) : (
              <>
                <h2>Say something</h2>
                <p>
                  Ask for a change, a review, or an explanation. Tools run in{' '}
                  {clip(dashboard.workspace.label, 40)}.
                </p>
              </>
            )
          }
        />

        {dashboard.chatId ? (
          <Composer
            draft={dashboard.draft}
            onDraftChange={dashboard.setDraft}
            run={dashboard.state.run}
            disabled={dashboard.busyAction || dashboard.state.closed}
            commands={dashboard.commands}
            onSend={dashboard.send}
            onStop={dashboard.abort}
          />
        ) : null}
      </main>

      <PendingDialogs
        state={dashboard.state}
        onToolDecision={dashboard.decideTool}
        onElicitationAnswer={dashboard.answerElicitation}
      />
    </div>
  );
}
