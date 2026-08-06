// Typed client for the dashboard API. Every mutation carries the per-process
// CSRF token issued by /api/bootstrap in a custom header; there is no CORS and
// no cookie, so a cross-site page can never make one of these calls.
import type {
  Accepted,
  APIError,
  Bootstrap,
  ChatRef,
  CommandInfo,
  ElicitationReply,
  ModelOption,
  ResolvedAgent,
  SessionSummary,
  Snapshot,
  SessionMeta,
  Stats,
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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && method !== 'HEAD') headers[CSRF_HEADER] = csrfToken;

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    redirect: 'error',
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

export const api = {
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
  openWorkspace(path: string): Promise<Workspace> {
    return request<Workspace>('POST', '/api/workspaces/open', { path });
  },
  resolveAgent(source: string, workspaceId: string, allowRemoteFetch = false): Promise<ResolvedAgent> {
    return request<ResolvedAgent>('POST', '/api/agents/resolve', {
      source,
      workspaceId,
      allowRemoteFetch,
    });
  },
  sessions(workspaceId: string): Promise<SessionSummary[]> {
    return request<SessionSummary[]>('GET', `/api/workspaces/${encodeURIComponent(workspaceId)}/sessions`);
  },
  // agentId may be '' — the server then uses its default agent ("coder").
  createChat(workspaceId: string, agentId = '', agentName = ''): Promise<ChatRef> {
    return request<ChatRef>('POST', '/api/chats', { workspaceId, agentId, agentName });
  },
  resumeChat(workspaceId: string, agentId: string, sessionId: string): Promise<ChatRef> {
    return request<ChatRef>('POST', '/api/chats/resume', { workspaceId, agentId, sessionId });
  },
  snapshot(chatId: string): Promise<Snapshot> {
    return request<Snapshot>('GET', `/api/chats/${encodeURIComponent(chatId)}`);
  },
  send(chatId: string, text: string, mode: 'normal' | 'steer' | 'followUp', idempotencyKey: string): Promise<Accepted> {
    return request<Accepted>('POST', `/api/chats/${encodeURIComponent(chatId)}/messages`, {
      text,
      mode,
      idempotencyKey,
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
