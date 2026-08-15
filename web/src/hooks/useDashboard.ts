import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError } from '@/api';
import type {
  Attachment,
  Bootstrap,
  ChatOptions,
  CommandInfo,
  ModelOption,
  SessionSummary,
  ToolOption,
  UpdateConfigRequest,
  Workspace,
} from '@/protocol.gen';
import type { SendMode } from '@/components/chat/Composer';
import { useChat } from './useChat';
import { useDraft } from './useDraft';
import { useWorkspacePreferences } from '@/preferences';
import { removeSessionSideViews } from '@/plugin-contributions';

interface DashboardRoute {
  sessionId: string | null;
  workspacePath: string | null;
  openSession: (sessionId: string, workspacePath: string) => void;
  leaveSession: () => void;
}

export function useDashboard(route: DashboardRoute, sessionsRevision = 0) {
  const [boot, setBoot] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState('');
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [workspacePath, setWorkspacePath] = useState('');
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [liveSessions, setLiveSessions] = useState<SessionSummary[]>([]);
  const [chatId, setChatId] = useState<string | null>(null);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [defaultOptions, setDefaultOptions] = useState<ChatOptions>({
    model: '', thinkingLevel: '', thinkingLevels: [], models: [],
  });
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  const [tools, setTools] = useState<ToolOption[]>([]);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState('');
  const [busyAction, setBusyAction] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const { prefs, recentWorkspaces, rememberWorkspace, forgetWorkspace } = useWorkspacePreferences(boot);
  const { state, connection, prepareChat, resnapshot } = useChat(chatId);
  const { draft, setDraft } = useDraft(activeSessionId);

  useEffect(() => {
    void api.bootstrap().then(async (result) => {
      setBoot(result);
      setWorkspacePath(prefs.recentWorkspaces[0] ?? result.workspaceHints?.[0]?.path ?? '');
      try {
        setDefaultOptions(await api.chatOptions());
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : 'model options could not be loaded');
      }
    }).catch((cause: unknown) =>
      setBootError(cause instanceof Error ? cause.message : 'failed to reach the server'),
    );
    // Preferences are intentionally sampled once during bootstrap.
  }, []);

  const guard = useCallback(async (action: () => Promise<void>, showBusy = true) => {
    if (showBusy) setBusyAction(true);
    setError('');
    try {
      await action();
    } catch (cause: unknown) {
      setError(cause instanceof ApiError ? cause.message : 'the request failed');
    } finally {
      if (showBusy) setBusyAction(false);
    }
  }, []);

  const refreshSessions = useCallback(async (nextWorkspace: Workspace) => {
    setSessions(await api.sessions(nextWorkspace.workspaceId));
  }, []);

  const refreshLiveSessions = useCallback(async () => {
    const refreshed = await api.liveSessions();
    setLiveSessions((current) => {
      const byId = new Map(refreshed.map((session) => [session.sessionId, session]));
      const retained = current
        .filter((session) => byId.has(session.sessionId))
        .map((session) => byId.get(session.sessionId)!);
      const retainedIds = new Set(retained.map((session) => session.sessionId));
      return [...retained, ...refreshed.filter((session) => !retainedIds.has(session.sessionId))];
    });
  }, []);

  // Dashboard-wide SSE invalidations keep sessions opened in other tabs and
  // projects current without polling.
  useEffect(() => {
    if (!boot) return;
    void refreshLiveSessions().catch(() => undefined);
    if (workspace) void refreshSessions(workspace).catch(() => undefined);
  }, [boot, refreshLiveSessions, refreshSessions, sessionsRevision, workspace]);

  // The selected chat stream makes its run state immediate while the global
  useEffect(() => {
    if (!activeSessionId) return;
    const applyRunState = (session: SessionSummary) =>
      session.sessionId === activeSessionId ? { ...session, runState: state.run.state } : session;
    setLiveSessions((current) => current.map(applyRunState));
    setSessions((current) => current.map(applyRunState));
  }, [activeSessionId, state.run.state]);

  const loadChatExtras = useCallback(async (nextChatId: string) => {
    const [nextModels, nextCommands, nextTools] = await Promise.all([
      api.models(nextChatId), api.commands(nextChatId), api.tools(nextChatId),
    ]);
    setModels(nextModels);
    setCommands(nextCommands);
    setTools(nextTools);
  }, []);

  const clearChat = useCallback(() => {
    setChatId(null);
    setActiveSessionId(null);
    setModels([]);
    setCommands([]);
    setTools([]);
    setAttachments([]);
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
      // Populate the conversation cache before swapping the active chat. This
      // keeps the previous tab visible until the destination can be rendered
      // in one frame instead of briefly showing an empty conversation.
      await prepareChat(nextChatId);
      setChatId(nextChatId);
      setActiveSessionId(sessionId);
      setDrawerOpen(false);
      await Promise.all([loadChatExtras(nextChatId), refreshSessions(nextWorkspace)]);
      void refreshLiveSessions().catch(() => undefined);
    },
    [loadChatExtras, prepareChat, refreshLiveSessions, refreshSessions],
  );

  const newChat = (initialMessage?: string) =>
    void guard(async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      const ref = await api.createChat(workspace.workspaceId);
      setChatId(ref.chatId);
      setActiveSessionId(ref.sessionId);
      setLiveSessions((current) => [
        ...current.filter((session) => session.sessionId !== ref.sessionId),
        {
          sessionId: ref.sessionId,
          title: 'New chat',
          workingDir: workspace.path,
          createdAt: new Date().toISOString(),
          messages: 0,
          live: true,
          chatId: ref.chatId,
          runState: 'idle',
        },
      ]);
      setDrawerOpen(false);
      route.openSession(ref.sessionId, workspace.path);
      await Promise.all([loadChatExtras(ref.chatId), refreshSessions(workspace)]);
      if (initialMessage) {
        await api.send(ref.chatId, initialMessage, 'normal');
        setDraft('');
      }
      void refreshLiveSessions().catch(() => undefined);
    });

  const resumeChat = (sessionId: string, targetWorkspacePath?: string) =>
    void guard(async () => {
      const path = targetWorkspacePath ?? workspace?.path;
      if (!path) throw new ApiError(400, 'no_workspace', 'choose a working directory first');

      let nextWorkspace = workspace;
      if (!nextWorkspace || nextWorkspace.path !== path) {
        nextWorkspace = await applyWorkspace(path, false);
      }
      const ref = await api.resumeChat(nextWorkspace.workspaceId, sessionId);
      await activateChat(ref.chatId, ref.sessionId, nextWorkspace);
      route.openSession(ref.sessionId, nextWorkspace.path);
    }, false);

  const closeLiveSession = (sessionId: string, liveChatId: string) => {
    removeSessionSideViews(sessionId);
    const closingIndex = liveSessions.findIndex((session) => session.sessionId === sessionId);
    const remaining = liveSessions.filter((session) => session.sessionId !== sessionId);
    const adjacentSession = closingIndex < 0
      ? undefined
      : remaining[Math.min(closingIndex, remaining.length - 1)];

    void guard(async () => {
      if (!liveChatId) return;
      await api.dispose(liveChatId);
      if (activeSessionId === sessionId) {
        if (adjacentSession) {
          let nextWorkspace = workspace;
          if (!nextWorkspace || nextWorkspace.path !== adjacentSession.workingDir) {
            nextWorkspace = await applyWorkspace(adjacentSession.workingDir, false);
          }
          const ref = await api.resumeChat(nextWorkspace.workspaceId, adjacentSession.sessionId);
          route.openSession(ref.sessionId, nextWorkspace.path);
          await activateChat(ref.chatId, ref.sessionId, nextWorkspace);
        } else {
          clearChat();
          route.leaveSession();
        }
      }
      await Promise.all([
        refreshLiveSessions(),
        workspace ? refreshSessions(workspace) : Promise.resolve(),
      ]);
    });
  };

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

      // Keep the current conversation mounted while the destination session
      // is restored. activateChat swaps it atomically once the new chat is
      // ready, avoiding a full-page-looking empty state between tabs.
      const nextWorkspace =
        workspace?.path === targetWorkspacePath
          ? workspace
          : await applyWorkspace(targetWorkspacePath, false);
      const ref = await api.resumeChat(nextWorkspace.workspaceId, targetSessionId);
      await activateChat(ref.chatId, ref.sessionId, nextWorkspace);
    }, false);
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

  const addAttachments = (files: File[]) => {
    if (!chatId || files.length === 0) return;
    void guard(async () => {
      setUploading(true);
      try {
        const room = Math.max(0, 4 - attachments.length);
        const uploaded = await Promise.all(files.slice(0, room).map((file) => api.uploadAttachment(chatId, file)));
        setAttachments((current) => [...current, ...uploaded]);
      } finally {
        setUploading(false);
      }
    });
  };

  const removeAttachment = (id: string) => {
    setAttachments((current) => current.filter((item) => item.id !== id));
    if (chatId) void api.deleteAttachment(chatId, id).catch(() => undefined);
  };

  const send = (text: string, mode: SendMode) =>
    void guard(async () => {
      if (!chatId) return;
      await api.send(chatId, text, mode, attachments.map((item) => item.id));
      setAttachments([]);
      setDraft('');
    });

  const patchConfig = (patch: { model?: string; thinkingLevel?: string }) =>
    void guard(async () => {
      const body: UpdateConfigRequest = {};
      if (patch.model !== undefined) body.model = patch.model;
      if (patch.thinkingLevel !== undefined) body.thinkingLevel = patch.thinkingLevel;
      if (!chatId) {
        setDefaultOptions(await api.updateChatOptions(body));
        return;
      }
      await api.updateConfig(chatId, body);
      const [, nextDefaults] = await Promise.all([loadChatExtras(chatId), api.chatOptions()]);
      setDefaultOptions(nextDefaults);
    });

  const setToolEnabled = (name: string, enabled: boolean) =>
    void guard(async () => {
      if (!chatId) return;
      const updated = await api.updateTool(chatId, name, enabled);
      setTools((current) => current.map((tool) => tool.name === updated.name ? updated : tool));
    });

  const runChatAction = (action: (currentChatId: string) => Promise<unknown>) =>
    void guard(async () => {
      if (chatId) await action(chatId);
    });

  const reorderLiveSessions = (draggedSessionId: string, targetSessionId: string) => {
    setLiveSessions((current) => {
      const from = current.findIndex((session) => session.sessionId === draggedSessionId);
      const to = current.findIndex((session) => session.sessionId === targetSessionId);
      if (from < 0 || to < 0 || from === to) return current;
      const reordered = [...current];
      const [dragged] = reordered.splice(from, 1);
      if (!dragged) return current;
      reordered.splice(to, 0, dragged);
      return reordered;
    });
  };

  const tabSessions = (() => {
    if (!activeSessionId || !chatId || !workspace) return liveSessions;
    if (liveSessions.some((session) => session.sessionId === activeSessionId)) return liveSessions;

    const stored = sessions.find((session) => session.sessionId === activeSessionId);
    return [
      ...liveSessions,
      {
        sessionId: activeSessionId,
        title: stored?.title || 'New chat',
        workingDir: workspace.path,
        createdAt: stored?.createdAt || '',
        messages: stored?.messages ?? 0,
        live: true,
        chatId,
        runState: state.run.state,
      },
    ];
  })();

  return {
    boot,
    bootError,
    workspace,
    workspacePath,
    setWorkspacePath,
    sessions,
    liveSessions: tabSessions,
    recentWorkspaces,
    chatId,
    activeSessionId,
    models: (chatId ? models : defaultOptions.models) ?? [],
    defaultModel: defaultOptions.model,
    defaultThinkingLevel: defaultOptions.thinkingLevel,
    defaultThinkingLevels: defaultOptions.thinkingLevels ?? [],
    commands,
    tools,
    attachments,
    uploading,
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
    reorderLiveSessions,
    send,
    addAttachments,
    removeAttachment,
    patchConfig,
    setToolEnabled,
    compact: () => runChatAction((id) => api.compact(id)),
    rename: (title: string) => runChatAction((id) => api.retitle(id, title)),
    abort: () => runChatAction((id) => api.abort(id)),
    decideTool: (decision: 'approve' | 'approveAlways' | 'reject', reason: string) =>
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
