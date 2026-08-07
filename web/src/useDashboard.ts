import { useCallback, useEffect, useRef, useState } from 'react';
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
import type { SendMode } from './components/Composer';
import { useChat } from './useChat';
import { useDraft } from './useDraft';
import { useWorkspacePreferences } from './preferences';

interface DashboardRoute {
  sessionId: string | null;
  workspacePath: string | null;
  openSession: (sessionId: string, workspacePath: string) => void;
  leaveSession: () => void;
}

export function useDashboard(route: DashboardRoute) {
  const [boot, setBoot] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState('');
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [workspacePath, setWorkspacePath] = useState('');
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [liveSessions, setLiveSessions] = useState<SessionSummary[]>([]);
  const [chatId, setChatId] = useState<string | null>(null);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  const [error, setError] = useState('');
  const [busyAction, setBusyAction] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const { prefs, recentWorkspaces, rememberWorkspace, forgetWorkspace } = useWorkspacePreferences(boot);
  const { state, connection, resnapshot } = useChat(chatId);
  const { draft, setDraft } = useDraft(activeSessionId);

  useEffect(() => {
    api
      .bootstrap()
      .then((result) => {
        setBoot(result);
        setWorkspacePath(prefs.recentWorkspaces[0] ?? result.workspaceHints?.[0]?.path ?? '');
      })
      .catch((cause: unknown) =>
        setBootError(cause instanceof Error ? cause.message : 'failed to reach the server'),
      );
    // Preferences are intentionally sampled once during bootstrap.
  }, []);

  const guard = useCallback(async (action: () => Promise<void>) => {
    setBusyAction(true);
    setError('');
    try {
      await action();
    } catch (cause: unknown) {
      setError(cause instanceof ApiError ? cause.message : 'the request failed');
    } finally {
      setBusyAction(false);
    }
  }, []);

  const refreshSessions = useCallback(async (nextWorkspace: Workspace) => {
    setSessions(await api.sessions(nextWorkspace.workspaceId));
  }, []);

  const refreshLiveSessions = useCallback(async () => {
    setLiveSessions(await api.liveSessions());
  }, []);

  // The current chat has an SSE stream, but sessions opened in another tab do
  // not. Poll this small global index so the cross-project list stays useful
  // without requiring the user to reopen each project.
  useEffect(() => {
    if (!boot) return;
    void refreshLiveSessions().catch(() => undefined);
    const timer = window.setInterval(() => {
      void refreshLiveSessions().catch(() => undefined);
    }, 3_000);
    return () => window.clearInterval(timer);
  }, [boot, refreshLiveSessions]);

  // SSE makes the selected session's status immediate; polling covers the
  // other live sessions running elsewhere.
  useEffect(() => {
    if (!activeSessionId) return;
    const applyRunState = (session: SessionSummary) =>
      session.sessionId === activeSessionId ? { ...session, runState: state.run.state } : session;
    setLiveSessions((current) => current.map(applyRunState));
    setSessions((current) => current.map(applyRunState));
  }, [activeSessionId, state.run.state]);

  const loadChatExtras = useCallback(async (nextChatId: string) => {
    const [nextModels, nextCommands] = await Promise.all([api.models(nextChatId), api.commands(nextChatId)]);
    setModels(nextModels);
    setCommands(nextCommands);
  }, []);

  const clearChat = useCallback(() => {
    setChatId(null);
    setActiveSessionId(null);
    setModels([]);
    setCommands([]);
  }, []);

  const applyWorkspace = useCallback(
    async (path: string, shouldClearChat: boolean) => {
      const nextWorkspace = await api.openWorkspace(path);
      setWorkspace(nextWorkspace);
      setWorkspacePath(nextWorkspace.path);
      if (shouldClearChat) clearChat();
      await refreshSessions(nextWorkspace);
      rememberWorkspace(nextWorkspace.path);
      return nextWorkspace;
    },
    [clearChat, refreshSessions, rememberWorkspace],
  );

  const openWorkspace = (path: string) => {
    setDrawerOpen(false);
    void guard(async () => {
      await applyWorkspace(path, true);
      route.leaveSession();
    });
  };

  const activateChat = useCallback(
    async (nextChatId: string, sessionId: string, nextWorkspace: Workspace) => {
      setChatId(nextChatId);
      setActiveSessionId(sessionId);
      setDrawerOpen(false);
      await Promise.all([loadChatExtras(nextChatId), refreshSessions(nextWorkspace)]);
      void refreshLiveSessions().catch(() => undefined);
    },
    [loadChatExtras, refreshLiveSessions, refreshSessions],
  );

  const newChat = () =>
    void guard(async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      const ref = await api.createChat(workspace.workspaceId);
      setChatId(ref.chatId);
      setActiveSessionId(ref.sessionId);
      setDrawerOpen(false);
      route.openSession(ref.sessionId, workspace.path);
      await Promise.all([loadChatExtras(ref.chatId), refreshSessions(workspace)]);
      void refreshLiveSessions().catch(() => undefined);
    });

  const resumeChat = (sessionId: string, targetWorkspacePath?: string) =>
    void guard(async () => {
      const path = targetWorkspacePath ?? workspace?.path;
      if (!path) throw new ApiError(400, 'no_workspace', 'choose a working directory first');

      let nextWorkspace = workspace;
      if (!nextWorkspace || nextWorkspace.path !== path) {
        nextWorkspace = await applyWorkspace(path, true);
      }
      const ref = await api.resumeChat(nextWorkspace.workspaceId, '', sessionId);
      setChatId(ref.chatId);
      setActiveSessionId(ref.sessionId);
      setDrawerOpen(false);
      route.openSession(ref.sessionId, nextWorkspace.path);
      await Promise.all([loadChatExtras(ref.chatId), refreshSessions(nextWorkspace)]);
      void refreshLiveSessions().catch(() => undefined);
    });

  const closeLiveSession = (sessionId: string, liveChatId: string) =>
    void guard(async () => {
      if (!liveChatId) return;
      await api.dispose(liveChatId);
      if (activeSessionId === sessionId) {
        clearChat();
        route.leaveSession();
      }
      await Promise.all([
        refreshLiveSessions(),
        workspace ? refreshSessions(workspace) : Promise.resolve(),
      ]);
    });

  // The URL is the source of truth for browser navigation and hard refreshes.
  // The workspace path in the query is stable across server restarts, unlike a
  // process-local workspace id.
  const syncedRoute = useRef('');
  const autoOpened = useRef(false);
  useEffect(() => {
    if (!boot) return;
    const routeKey = route.sessionId ? `${route.workspacePath ?? ''}\n${route.sessionId}` : 'home';
    if (syncedRoute.current === routeKey) return;
    syncedRoute.current = routeKey;

    if (!route.sessionId) {
      clearChat();
      if (!workspace && !autoOpened.current) {
        autoOpened.current = true;
        const last = prefs.recentWorkspaces[0];
        if (last) void applyWorkspace(last, true).catch(() => forgetWorkspace(last));
      }
      return;
    }

    const targetSessionId = route.sessionId;
    const targetWorkspacePath = route.workspacePath;
    void guard(async () => {
      if (!targetWorkspacePath) {
        clearChat();
        throw new ApiError(400, 'missing_workspace', 'this session URL does not include a workspace');
      }
      if (activeSessionId === targetSessionId && chatId && workspace?.path === targetWorkspacePath) return;

      // Do not leave the previous conversation visible under a new session URL
      // while its workspace and snapshot are being restored.
      clearChat();
      const nextWorkspace =
        workspace?.path === targetWorkspacePath
          ? workspace
          : await applyWorkspace(targetWorkspacePath, false);
      const ref = await api.resumeChat(nextWorkspace.workspaceId, '', targetSessionId);
      await activateChat(ref.chatId, ref.sessionId, nextWorkspace);
    });
  }, [
    activeSessionId,
    activateChat,
    applyWorkspace,
    boot,
    chatId,
    clearChat,
    forgetWorkspace,
    guard,
    prefs.recentWorkspaces,
    route.sessionId,
    route.workspacePath,
    workspace,
  ]);

  const send = (text: string, mode: SendMode) =>
    void guard(async () => {
      if (!chatId) return;
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      await api.send(chatId, text, mode, key);
      setDraft('');
    });

  const patchConfig = (patch: { model?: string; thinkingLevel?: string; posture?: Posture }) =>
    void guard(async () => {
      if (!chatId) return;
      const body: UpdateConfigRequest = { confirmAutoApprove: patch.posture === 'autonomous' };
      if (patch.model !== undefined) body.model = patch.model;
      if (patch.thinkingLevel !== undefined) body.thinkingLevel = patch.thinkingLevel;
      if (patch.posture !== undefined) body.posture = patch.posture;
      await api.updateConfig(chatId, body);
      await loadChatExtras(chatId);
    });

  const runChatAction = (action: (currentChatId: string) => Promise<unknown>) =>
    void guard(async () => {
      if (chatId) await action(chatId);
    });

  return {
    boot,
    bootError,
    workspace,
    workspacePath,
    setWorkspacePath,
    sessions,
    liveSessions,
    recentWorkspaces,
    chatId,
    activeSessionId,
    models,
    commands,
    error,
    busyAction,
    drawerOpen,
    setDrawerOpen,
    state,
    connection,
    resnapshot,
    draft,
    setDraft,
    openWorkspace,
    newChat,
    resumeChat,
    closeLiveSession,
    send,
    patchConfig,
    compact: () => runChatAction((id) => api.compact(id)),
    rename: (title: string) => runChatAction((id) => api.retitle(id, title)),
    abort: () => runChatAction((id) => api.abort(id)),
    decideTool: (decision: 'approve' | 'approveAlways' | 'approveSession' | 'reject', reason: string) =>
      runChatAction(async (id) => {
        const request = state.confirmations[0];
        if (request) await api.confirmTool(id, { toolCallId: request.toolCallId, decision, reason });
      }),
    answerElicitation: (action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown>) =>
      runChatAction(async (id) => {
        const request = state.elicitations[0];
        if (request) await api.answerElicitation(id, { elicitationId: request.elicitationId, action, content });
      }),
  };
}
