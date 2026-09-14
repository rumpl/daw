import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError, CHAT_OPTIONS_CHANGE_EVENT } from '@/api';
import type {
  Attachment,
  Bootstrap,
  ChatOptions,
  ChatRef,
  CommandInfo,
  ExecutionTarget,
  ModelOption,
  ProjectFolder,
  SessionSummary,
  UpdateConfigRequest,
  Workspace,
  SandboxProvisioning,
} from '@/protocol.gen';
import type { SendMode } from '@/components/chat/Composer';
import { useChat } from './useChat';
import { useWorkspacePreferences } from '@/preferences';
import { removeSessionSideViews } from '@/plugin-contributions';
import { loadOpenSessionTabIds, saveOpenSessionTabIds } from '@/session-tabs-storage';
import { clearDraft, migrateDraft } from '@/hooks/useDraft';

interface DashboardRoute {
  sessionId: string | null;
  workspacePath: string | null;
  openSession: (sessionId: string, workspacePath: string) => void;
  leaveSession: () => void;
}

export function useDashboard(
  route: DashboardRoute,
  sessionsRevision = 0,
  provisioningEvents: Record<string, SandboxProvisioning> = {},
  clearProvisioningEvent: (operationId: string) => void = () => undefined,
) {
  const [boot, setBoot] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState('');
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [workspacePath, setWorkspacePath] = useState('');
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [allSessions, setAllSessions] = useState<SessionSummary[]>([]);
  const [projectFolders, setProjectFolders] = useState<ProjectFolder[]>([]);
  const [liveSessions, setLiveSessions] = useState<SessionSummary[]>([]);
  const [draftSessions, setDraftSessions] = useState<SessionSummary[]>([]);
  const [openTabSessionIds, setOpenTabSessionIds] = useState<string[]>(loadOpenSessionTabIds);
  const [chatId, setChatId] = useState<string | null>(null);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [defaultOptions, setDefaultOptions] = useState<ChatOptions>({
    model: '', thinkingLevel: '', thinkingLevels: [], models: [], tools: [],
  });
  const [executionTarget, setExecutionTargetState] = useState<ExecutionTarget>('host');
  const [commands, setCommands] = useState<CommandInfo[]>([]);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState('');
  const [busyAction, setBusyAction] = useState(false);
  const [provisioningOperation, setProvisioningOperation] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const creatingDraftChat = useRef<Promise<ChatRef> | null>(null);

  const provisioning = provisioningOperation ? provisioningEvents[provisioningOperation] ?? null : null;

  const withProvisioning = async <T,>(action: (operationId: string) => Promise<T>): Promise<T> => {
    const operationId = crypto.randomUUID();
    clearProvisioningEvent(operationId);
    setProvisioningOperation(operationId);
    try {
      return await action(operationId);
    } finally {
      window.setTimeout(() => {
        setProvisioningOperation((current) => current === operationId ? null : current);
        clearProvisioningEvent(operationId);
      }, 800);
    }
  };

  const { prefs, recentWorkspaces, rememberWorkspace, forgetWorkspace } = useWorkspacePreferences(boot);
  const { state, connection, prepareChat, resnapshot } = useChat(chatId);

  useEffect(() => {
    void api.bootstrap().then(async (result) => {
      setBoot(result);
      setProjectFolders(result.projectFolders ?? []);
      setExecutionTargetState(result.defaultExecutionTarget);
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

  useEffect(() => {
    const refreshOptions = () => { void api.chatOptions().then(setDefaultOptions).catch(() => undefined); };
    window.addEventListener(CHAT_OPTIONS_CHANGE_EVENT, refreshOptions);
    return () => window.removeEventListener(CHAT_OPTIONS_CHANGE_EVENT, refreshOptions);
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

  const refreshAllSessions = useCallback(async () => {
    setAllSessions(await api.allSessions());
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

  // Dashboard-wide SSE invalidations keep backend session metadata and projects
  // current without changing which tabs this browser has explicitly opened.
  useEffect(() => {
    if (!boot) return;
    void refreshLiveSessions().catch(() => undefined);
    void refreshAllSessions().catch(() => undefined);
    if (workspace) void refreshSessions(workspace).catch(() => undefined);
  }, [boot, refreshAllSessions, refreshLiveSessions, refreshSessions, sessionsRevision, workspace]);

  // The selected chat stream makes its run state immediate while the global
  useEffect(() => {
    if (!activeSessionId) return;
    const applyRunState = (session: SessionSummary) =>
      session.sessionId === activeSessionId ? { ...session, runState: state.run.state } : session;
    setLiveSessions((current) => current.map(applyRunState));
    setSessions((current) => current.map(applyRunState));
  }, [activeSessionId, state.run.state]);

  const starSession = useCallback(async (sessionId: string, starred: boolean) => {
    const apply = (session: SessionSummary) => session.sessionId === sessionId ? { ...session, starred } : session;
    setSessions((current) => current.map(apply));
    setAllSessions((current) => current.map(apply));
    setLiveSessions((current) => current.map(apply));
    try {
      await api.starSession(sessionId, starred);
    } catch (cause) {
      const revert = (session: SessionSummary) => session.sessionId === sessionId ? { ...session, starred: !starred } : session;
      setSessions((current) => current.map(revert));
      setAllSessions((current) => current.map(revert));
      setLiveSessions((current) => current.map(revert));
      setError(cause instanceof Error ? cause.message : 'the session could not be updated');
    }
  }, []);

  const updateProjectFolders = useCallback(async (folders: ProjectFolder[]) => {
    const previous = projectFolders;
    setProjectFolders(folders);
    try {
      await api.updateProjectFolders(folders);
      setBoot((current) => current ? { ...current, projectFolders: folders } : current);
    } catch (cause) {
      setProjectFolders(previous);
      setError(cause instanceof Error ? cause.message : 'project folders could not be updated');
    }
  }, [projectFolders]);

  const loadChatExtras = useCallback(async (nextChatId: string) => {
    const [nextModels, nextCommands] = await Promise.all([
      api.models(nextChatId), api.commands(nextChatId),
    ]);
    setModels(nextModels);
    setCommands(nextCommands);
  }, []);

  const clearChat = useCallback(() => {
    creatingDraftChat.current = null;
    setChatId(null);
    setActiveSessionId(null);
    setModels([]);
    setCommands([]);
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

  const openDraftTab = useCallback((nextWorkspace: Workspace) => {
    const sessionId = `draft:${crypto.randomUUID()}`;
    const draft: SessionSummary = {
      sessionId,
      title: 'New chat',
      workingDir: nextWorkspace.path,
      createdAt: new Date().toISOString(),
      messages: 0,
      starred: false,
      executionTarget,
      live: false,
      runState: 'idle',
    };
    setDraftSessions((current) => [...current, draft]);
    clearChat();
    setActiveSessionId(sessionId);
    setDrawerOpen(false);
  }, [clearChat, executionTarget]);

  const newChatForWorkspace = (path: string) => {
    void guard(async () => {
      const nextWorkspace = await applyWorkspace(path, false);
      openDraftTab(nextWorkspace);
      route.leaveSession();
    }, false);
  };

  const addWorkspace = (path: string) => {
    void guard(async () => {
      const added = await api.openWorkspace(path);
      rememberWorkspace(added.path);
      setWorkspacePath(added.path);
      setBoot((current) => current ? {
        ...current,
        workspaceHints: [
          { path: added.path, label: added.label },
          ...(current.workspaceHints ?? []).filter((hint) => hint.path !== added.path),
        ],
      } : current);
    }, false);
  };

  const removeWorkspace = (path: string) => {
    void guard(async () => {
      await api.removeWorkspace(path);
      forgetWorkspace(path);
      setBoot((current) => current ? {
        ...current,
        workspaceHints: (current.workspaceHints ?? []).filter((hint) => hint.path !== path),
      } : current);
    }, false);
  };

  const updateOpenTabSessionIds = useCallback((update: (current: string[]) => string[]) => {
    setOpenTabSessionIds((current) => {
      const next = update(current);
      if (next !== current) saveOpenSessionTabIds(next);
      return next;
    });
  }, []);

  const activateChat = useCallback(
    async (nextChatId: string, sessionId: string, nextWorkspace: Workspace) => {
      // Populate the conversation cache before swapping the active chat. This
      // keeps the previous tab visible until the destination can be rendered
      // in one frame instead of briefly showing an empty conversation.
      await prepareChat(nextChatId);
      updateOpenTabSessionIds((current) => current.includes(sessionId) ? current : [...current, sessionId]);
      setChatId(nextChatId);
      setActiveSessionId(sessionId);
      setDrawerOpen(false);
      await Promise.all([loadChatExtras(nextChatId), refreshSessions(nextWorkspace), refreshAllSessions()]);
      void refreshLiveSessions().catch(() => undefined);
    },
    [loadChatExtras, prepareChat, refreshAllSessions, refreshLiveSessions, refreshSessions, updateOpenTabSessionIds],
  );

  const createDraftChat = (persistImmediately = false) => {
    if (creatingDraftChat.current) return creatingDraftChat.current;
    const creation = (async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      const replacedDraftId = activeSessionId?.startsWith('draft:') ? activeSessionId : null;
      const ref = await withProvisioning((operationId) =>
        api.createChat(workspace.workspaceId, undefined, executionTarget, operationId, persistImmediately));
      if (replacedDraftId) {
        migrateDraft(replacedDraftId, ref.sessionId);
        setDraftSessions((current) => current.filter((session) => session.sessionId !== replacedDraftId));
      }
      setChatId(ref.chatId);
      setActiveSessionId(ref.sessionId);
      updateOpenTabSessionIds((current) => {
        const withoutDraft = replacedDraftId ? current.filter((id) => id !== replacedDraftId) : current;
        return withoutDraft.includes(ref.sessionId) ? withoutDraft : [...withoutDraft, ref.sessionId];
      });
      setLiveSessions((current) => [
        ...current.filter((session) => session.sessionId !== ref.sessionId),
        {
          sessionId: ref.sessionId,
          title: 'New chat',
          workingDir: workspace.path,
          createdAt: new Date().toISOString(),
          messages: 0,
          starred: false,
          executionTarget,
          live: true,
          chatId: ref.chatId,
          runState: 'idle',
        },
      ]);
      setDrawerOpen(false);
      route.openSession(ref.sessionId, workspace.path);
      await Promise.all([loadChatExtras(ref.chatId), refreshSessions(workspace)]);
      void refreshLiveSessions().catch(() => undefined);
      return ref;
    })();
    creatingDraftChat.current = creation;
    const clearCreatingChat = () => {
      if (creatingDraftChat.current === creation) creatingDraftChat.current = null;
    };
    void creation.then(clearCreatingChat, clearCreatingChat);
    return creation;
  };

  const newChat = (initialMessage?: string, persistImmediately = false) => {
    // Empty chats stay client-side until their first message or attachment,
    // but each one has a stable identity so it behaves like a real tab.
    if (initialMessage === undefined) {
      if (!workspace) return Promise.resolve(false);
      openDraftTab(workspace);
      route.leaveSession();
      return Promise.resolve(true);
    }

    let created: ChatRef | false = false;
    return guard(async () => {
      const draftSessionId = activeSessionId;
      const ref = await createDraftChat(persistImmediately);
      if (initialMessage) {
        await api.send(ref.chatId, initialMessage, 'normal');
        clearDraft(draftSessionId);
        clearDraft(ref.sessionId);
      }
      created = ref;
    }).then(() => created);
  };

  const newSplitChat = () => {
    let created: ChatRef | false = false;
    return guard(async () => {
      if (!workspace) throw new ApiError(400, 'no_workspace', 'choose a working directory first');
      created = await withProvisioning((operationId) =>
        api.createChat(workspace.workspaceId, undefined, executionTarget, operationId, true));
      void refreshLiveSessions().catch(() => undefined);
    }).then(() => created);
  };

  const resumeChat = (sessionId: string, targetWorkspacePath?: string) => {
    if (sessionId.startsWith('draft:')) {
      const draft = draftSessions.find((session) => session.sessionId === sessionId);
      if (!draft) return;
      clearChat();
      setActiveSessionId(sessionId);
      setExecutionTargetState(draft.executionTarget ?? executionTarget);
      setDrawerOpen(false);
      route.leaveSession();
      return;
    }
    void guard(async () => {
      const path = targetWorkspacePath ?? workspace?.path;
      if (!path) throw new ApiError(400, 'no_workspace', 'choose a working directory first');

      let nextWorkspace = workspace;
      if (!nextWorkspace || nextWorkspace.path !== path) {
        nextWorkspace = await applyWorkspace(path, false);
      }
      const ref = await withProvisioning((operationId) =>
        api.resumeChat(nextWorkspace.workspaceId, sessionId, operationId));
      await activateChat(ref.chatId, ref.sessionId, nextWorkspace);
      route.openSession(ref.sessionId, nextWorkspace.path);
    }, false);
  };

  const closeSessionTab = (sessionId: string) => {
    removeSessionSideViews(sessionId);
    const sessionMetadata = new Map(
      [...allSessions, ...sessions, ...liveSessions].map((session) => [session.sessionId, session]),
    );
    const visibleSessions = [
      ...openTabSessionIds.flatMap((id) => {
        const session = sessionMetadata.get(id);
        return session ? [session] : [];
      }),
      ...draftSessions,
    ];
    const closingIndex = visibleSessions.findIndex((session) => session.sessionId === sessionId);
    const remaining = visibleSessions.filter((session) => session.sessionId !== sessionId);
    const adjacentSession = closingIndex < 0
      ? undefined
      : remaining[Math.min(closingIndex, remaining.length - 1)];

    if (sessionId.startsWith('draft:')) {
      setDraftSessions((current) => current.filter((session) => session.sessionId !== sessionId));
    } else {
      updateOpenTabSessionIds((current) => current.filter((id) => id !== sessionId));
    }
    if (activeSessionId !== sessionId) return;
    clearChat();
    if (!adjacentSession) {
      route.leaveSession();
    } else if (adjacentSession.sessionId.startsWith('draft:')) {
      setActiveSessionId(adjacentSession.sessionId);
      setExecutionTargetState(adjacentSession.executionTarget ?? executionTarget);
      route.leaveSession();
    } else {
      route.openSession(adjacentSession.sessionId, adjacentSession.workingDir);
    }
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
      if (!activeSessionId?.startsWith('draft:')) clearChat();
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
      const ref = await withProvisioning((operationId) =>
        api.resumeChat(nextWorkspace.workspaceId, targetSessionId, operationId));
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
    if (!workspace || files.length === 0) return;
    void guard(async () => {
      setUploading(true);
      try {
        const targetChatId = chatId ?? (await createDraftChat()).chatId;
        const room = Math.max(0, 4 - attachments.length);
        const uploaded = await Promise.all(files.slice(0, room).map((file) => api.uploadAttachment(targetChatId, file)));
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

  const send = (text: string, mode: SendMode) => {
    let sent = false;
    return guard(async () => {
      if (!chatId) return;
      await api.send(chatId, text, mode, attachments.map((item) => item.id));
      setAttachments([]);
      sent = true;
    }).then(() => sent);
  };

  const selectExecutionTarget = (target: ExecutionTarget) =>
    void guard(async () => {
      const saved = await api.updateExecutionTarget(target);
      setExecutionTargetState(saved.executionTarget);
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

  const refreshChatOptions = useCallback(async () => {
    setDefaultOptions(await api.chatOptions());
  }, []);

  const setToolEnabled = (name: string, enabled: boolean) =>
    void guard(async () => {
      const updated = await api.updateDefaultTool(name, enabled);
      setDefaultOptions((current) => ({
        ...current,
        tools: (current.tools ?? []).map((tool) => tool.name === updated.name ? updated : tool),
      }));
      window.dispatchEvent(new Event(CHAT_OPTIONS_CHANGE_EVENT));
    });

  const runChatAction = (action: (currentChatId: string) => Promise<unknown>) =>
    void guard(async () => {
      if (chatId) await action(chatId);
    });

  const reorderLiveSessions = (draggedSessionId: string, targetSessionId: string) => {
    updateOpenTabSessionIds((current) => {
      const from = current.indexOf(draggedSessionId);
      const to = current.indexOf(targetSessionId);
      if (from < 0 || to < 0 || from === to) return current;
      const reordered = [...current];
      const [dragged] = reordered.splice(from, 1);
      if (!dragged) return current;
      reordered.splice(to, 0, dragged);
      return reordered;
    });
  };

  const sessionMetadata = new Map(
    [...allSessions, ...sessions, ...liveSessions].map((session) => [session.sessionId, session]),
  );
  if (activeSessionId && chatId && workspace && !sessionMetadata.has(activeSessionId)) {
    const stored = sessions.find((session) => session.sessionId === activeSessionId);
    sessionMetadata.set(activeSessionId, {
      sessionId: activeSessionId,
      title: stored?.title || 'New chat',
      workingDir: workspace.path,
      createdAt: stored?.createdAt || '',
      messages: stored?.messages ?? 0,
      starred: stored?.starred ?? false,
      executionTarget: stored?.executionTarget ?? state.meta?.executionTarget,
      live: true,
      chatId,
      runState: state.run.state,
    });
  }

  const visibleTabSessions = [
    ...openTabSessionIds.flatMap((id) => {
      const session = sessionMetadata.get(id);
      return session ? [session] : [];
    }),
    ...draftSessions,
  ];

  return {
    boot,
    bootError,
    workspace,
    workspacePath,
    setWorkspacePath,
    sessions,
    allSessions,
    starSession,
    liveSessions: visibleTabSessions,
    recentWorkspaces,
    projectFolders,
    updateProjectFolders,
    chatId,
    activeSessionId,
    models: (chatId ? models : defaultOptions.models) ?? [],
    defaultModel: defaultOptions.model,
    defaultThinkingLevel: defaultOptions.thinkingLevel,
    defaultThinkingLevels: defaultOptions.thinkingLevels ?? [],
    executionTargets: boot?.executionTargets ?? [],
    executionTarget,
    setExecutionTarget: selectExecutionTarget,
    commands,
    tools: defaultOptions.tools ?? [],
    attachments,
    uploading,
    error,
    busyAction,
    provisioning,
    drawerOpen,
    setDrawerOpen,
    state,
    connection,
    resnapshot,
    openWorkspace,
    addWorkspace,
    removeWorkspace,
    newChat,
    newSplitChat,
    newChatForWorkspace,
    resumeChat,
    closeSessionTab,
    reorderLiveSessions,
    send,
    addAttachments,
    removeAttachment,
    patchConfig,
    refreshChatOptions,
    setToolEnabled,
    compact: () => runChatAction((id) => api.compact(id)),
    rename: (title: string) => runChatAction((id) => api.retitle(id, title)),
    abort: () => runChatAction((id) => api.abort(id)),
  };
}
