// Typed client for the dashboard API. Every mutation carries the per-process
// CSRF token issued by /api/bootstrap in a custom header; there is no CORS and
// no cookie, so a cross-site page can never make one of these calls.
import type {
  Accepted,
  APIError,
  Attachment,
  Bootstrap,
  ChatOptions,
  ChatRef,
  CommandInfo,
  ElicitationReply,
  ManagedPlugin,
  ModelOption,
  ModelsGatewayConfig,
  PluginCatalog,
  PluginConfiguration,
  PluginManagementCatalog,
  SessionSummary,
  StoredSession,
  StoredSessionItems,
  Snapshot,
  SessionMeta,
  Stats,
  ToolOption,
  ToolConfirmationReply,
  UpdateConfigRequest,
  Workspace,
} from './protocol.gen';

export const CSRF_HEADER = 'X-DAW-CSRF';

let csrfToken = '';

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

export interface RequestOptions {
  signal?: AbortSignal;
}

export async function request<T>(method: string, path: string, body?: unknown, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && method !== 'HEAD') headers[CSRF_HEADER] = csrfToken;

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    redirect: 'error',
    signal: options.signal,
  });

  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }
  if (!res.ok) {
    const err = (parsed ?? {}) as Partial<APIError>;
    throw new ApiError(res.status, err.code ?? 'unknown', err.error ?? `request failed (${res.status})`);
  }
  return parsed as T;
}

export type PluginManagement = ManagedPlugin;

export const CHAT_OPTIONS_CHANGE_EVENT = 'dawui:chat-options-change';

export const api = {
  request,
  setCsrfToken(token: string): void {
    csrfToken = token;
  },
  csrfToken(): string {
    return csrfToken;
  },
  async bootstrap(): Promise<Bootstrap> {
    const b = await request<Bootstrap>('GET', '/api/bootstrap');
    csrfToken = b.csrfToken;
    return b;
  },
  plugins(): Promise<PluginCatalog> {
    return request<PluginCatalog>('GET', '/api/plugins');
  },
  pluginManagement(): Promise<PluginManagementCatalog> {
    return request<PluginManagementCatalog>('GET', '/api/plugin-management');
  },
  managePlugin(pluginId: string, action: 'start' | 'stop' | 'enable' | 'disable'): Promise<PluginManagement> {
    return request<PluginManagement>('POST', `/api/plugins/${encodeURIComponent(pluginId)}/${action}`);
  },
  deletePlugin(pluginId: string): Promise<void> {
    return request<void>('DELETE', `/api/plugins/${encodeURIComponent(pluginId)}`);
  },
  pluginConfiguration(pluginId: string): Promise<PluginConfiguration> {
    return request<PluginConfiguration>('GET', `/api/plugins/${encodeURIComponent(pluginId)}/config`);
  },
  updatePluginConfiguration(pluginId: string, values: Record<string, unknown>): Promise<PluginConfiguration> {
    return request<PluginConfiguration>('PUT', `/api/plugins/${encodeURIComponent(pluginId)}/config`, { values });
  },
  openWorkspace(path: string): Promise<Workspace> {
    return request<Workspace>('POST', '/api/workspaces/open', { path });
  },
  liveSessions(): Promise<SessionSummary[]> {
    return request<SessionSummary[]>('GET', '/api/sessions/live');
  },
  sessions(workspaceId: string): Promise<SessionSummary[]> {
    return request<SessionSummary[]>('GET', `/api/workspaces/${encodeURIComponent(workspaceId)}/sessions`);
  },
  session(workspaceId: string, sessionId: string, options: RequestOptions = {}): Promise<StoredSession> {
    return request<StoredSession>('GET', `/api/workspaces/${encodeURIComponent(workspaceId)}/sessions/${encodeURIComponent(sessionId)}`, undefined, options);
  },
  sessionItems(workspaceId: string, sessionId: string, options: { offset?: number; limit?: number; signal?: AbortSignal } = {}): Promise<StoredSessionItems> {
    const query = new URLSearchParams();
    if (options.offset !== undefined) query.set('offset', String(options.offset));
    if (options.limit !== undefined) query.set('limit', String(options.limit));
    const suffix = query.size ? `?${query}` : '';
    return request<StoredSessionItems>('GET', `/api/workspaces/${encodeURIComponent(workspaceId)}/sessions/${encodeURIComponent(sessionId)}/items${suffix}`, undefined, { signal: options.signal });
  },
  modelsGateway(): Promise<ModelsGatewayConfig> {
    return request<ModelsGatewayConfig>('GET', '/api/settings/models-gateway');
  },
  updateModelsGateway(url: string): Promise<ModelsGatewayConfig> {
    return request<ModelsGatewayConfig>('PUT', '/api/settings/models-gateway', { url });
  },
  chatOptions(): Promise<ChatOptions> {
    return request<ChatOptions>('GET', '/api/chat-options');
  },
  updateChatOptions(patch: UpdateConfigRequest): Promise<ChatOptions> {
    return request<ChatOptions>('PATCH', '/api/chat-options', patch);
  },
  updateDefaultTool(name: string, enabled: boolean): Promise<ToolOption> {
    return request<ToolOption>('PATCH', `/api/chat-options/tools/${encodeURIComponent(name)}`, { enabled });
  },
  createChat(workspaceId: string, executionLocationId?: string): Promise<ChatRef> {
    return request<ChatRef>('POST', '/api/chats', { workspaceId, ...(executionLocationId ? { executionLocationId } : {}) });
  },
  resumeChat(workspaceId: string, sessionId: string): Promise<ChatRef> {
    return request<ChatRef>('POST', '/api/chats/resume', { workspaceId, sessionId });
  },
  snapshot(chatId: string): Promise<Snapshot> {
    return request<Snapshot>('GET', `/api/chats/${encodeURIComponent(chatId)}`);
  },
  uploadAttachment(chatId: string, file: File, options: RequestOptions = {}): Promise<Attachment> {
    const body = new FormData();
    body.append('file', file, file.name);
    return fetch(`/api/chats/${encodeURIComponent(chatId)}/attachments`, {
      method: 'POST',
      headers: { Accept: 'application/json', [CSRF_HEADER]: csrfToken },
      body,
      credentials: 'same-origin',
      redirect: 'error',
      signal: options.signal,
    }).then(async (res) => {
      const parsed = await res.json().catch(() => ({})) as Partial<APIError> & Partial<Attachment>;
      if (!res.ok) throw new ApiError(res.status, parsed.code ?? 'unknown', parsed.error ?? `upload failed (${res.status})`);
      return parsed as Attachment;
    });
  },
  deleteAttachment(chatId: string, attachmentId: string): Promise<void> {
    return request<void>('DELETE', `/api/chats/${encodeURIComponent(chatId)}/attachments/${encodeURIComponent(attachmentId)}`);
  },
  send(chatId: string, text: string, mode: 'normal' | 'steer' | 'followUp', attachments: string[] = []): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/messages`, {
      text,
      mode,
      attachments,
    });
  },
  abort(chatId: string): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/abort`);
  },
  updateConfig(chatId: string, patch: UpdateConfigRequest): Promise<SessionMeta> {
    return request<SessionMeta>('PATCH', `/api/chats/${encodeURIComponent(chatId)}/config`, patch);
  },
  models(chatId: string): Promise<ModelOption[]> {
    return request<ModelOption[]>('GET', `/api/chats/${encodeURIComponent(chatId)}/models`);
  },
  commands(chatId: string): Promise<CommandInfo[]> {
    return request<CommandInfo[]>('GET', `/api/chats/${encodeURIComponent(chatId)}/commands`);
  },
  confirmTool(chatId: string, reply: ToolConfirmationReply): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/tool-confirmation`, reply);
  },
  answerElicitation(chatId: string, reply: ElicitationReply): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/elicitation`, reply);
  },
  retitle(chatId: string, title: string): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/retitle`, { title });
  },
  compact(chatId: string): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/compact`);
  },
  stats(chatId: string): Promise<Stats> {
    return request<Stats>('GET', `/api/chats/${encodeURIComponent(chatId)}/stats`);
  },
  dispose(chatId: string): Promise<Accepted> {
    return request<Accepted>('DELETE', `/api/chats/${encodeURIComponent(chatId)}`);
  },
};
