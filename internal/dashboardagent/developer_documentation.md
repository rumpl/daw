# Docker Agent Dashboard developer API

This is the complete developer contract for dashboard plugin API version 1.
Plugins are trusted same-origin browser ES modules. The global plugin directory
is the absolute path in `DAWUI_PLUGIN_DIR` (default
`~/.cagent/dawui/plugins`). API URLs are relative to the dashboard origin.

## HTTP conventions

- All responses are JSON except SSE and fingerprinted plugin assets.
- Call APIs through `context.api`; its mutation methods attach the current
  `X-DAW-CSRF` token. The generic form is
  `api.request(method, path, body?, {signal?})`.
- Browser credentials are same-origin. Redirects are rejected.
- Errors have `{error: string, code: string, details?: string}` and cause the
  client to throw `ApiError {message, code, status}`.
- IDs beginning with `ws_`, `ag_`, and `chat_` are process-local opaque IDs.
  Never construct or persist them across server restarts. Session IDs are
  stable in docker-agent's session database.
- Mutations require same-origin metadata and the CSRF header. Plugin code gets
  this automatically through `context.api`.
- Request bodies reject unknown fields and content over 256 KiB.

## Complete backend endpoint list

### Service and plugin discovery

- `GET /api/health` → `200 Health`
  - No setup required. Liveness and process uptime.
- `GET /api/bootstrap` → `200 Bootstrap`
  - Initializes the API client CSRF token and returns paths, defaults, notices,
    workspace hints, and model availability.
- `GET /api/plugins` → `200 PluginCatalog`
  - Lists all valid global plugins plus validation diagnostics.
- `GET /api/plugins/{pluginId}/assets/{fingerprint}/{path...}` → asset bytes
  - URL comes from `Plugin.entryUrl`/`styleUrl`. It is immutable and may be
    cached for one year. A stale fingerprint or invalid path returns 404.

### Workspaces

- `POST /api/workspaces/open`
  - Body: `{path: string}` where path is absolute and inside the server user's home directory.
  - Returns `200 Workspace`. The opaque `workspaceId` is used in later calls.

Every chat uses the dashboard's SDK-built coding agent. There is no agent
selection or agent-resolution API.

### Sessions and live chats

- `GET /api/events[?lastEventId=N]` → dashboard-wide SSE stream
  - Sends low-volume `snapshot`, `sessions_changed`, `plugins_changed`, and
    `gap` invalidations. Reconnect with the last applied sequence; clients
    refresh the corresponding authoritative REST resources.

- `GET /api/sessions/live` → `200 SessionSummary[]`
  - Every session currently owned by this server across all workspaces.
- `GET /api/workspaces/{workspaceId}/sessions` → `200 SessionSummary[]`
  - Stored sessions for one opened workspace, including live status.
- `POST /api/chats`
  - Body: `{workspaceId: string}`. Returns `201 ChatRef`.
- `POST /api/chats/resume`
  - Body: `{workspaceId: string, sessionId: string}`.
  - Returns `201 ChatRef`. A session can have only one live runtime in this
    server; attaching to an already-live session returns that owner.
- `GET /api/chats/{id}` → `200 Snapshot`
  - Complete authoritative chat state for resnapshot/reconciliation.
- `GET /api/chats/{id}/events[?lastEventId=N]` → SSE stream
  - Each `data:` frame is one `Event`. Event `seq` is monotonic per live chat.
    Reconnect with the last applied sequence. A replay gap produces a fresh
    snapshot/gap recovery. The server sends heartbeat comments.
- `POST /api/chats/{id}/messages`
  - Body: `{text, mode, idempotencyKey}`.
  - `mode`: `normal` starts while idle, `steer` injects into a running turn,
    `followUp` queues a later turn. Returns `202 Accepted`.
- `POST /api/chats/{id}/abort` → `202 Accepted`
  - Cancels the run and clears queued steering/follow-ups.
- `PATCH /api/chats/{id}/config`
  - Body: `{model?: string, thinkingLevel?: string}`.
  - Only valid while idle. Returns `200 SessionMeta`.
- `GET /api/chats/{id}/models` → `200 ModelOption[]`
- `GET /api/chats/{id}/commands` → `200 CommandInfo[]`
- `POST /api/chats/{id}/tool-confirmation`
  - Body: `{toolCallId, decision, reason}`. Decisions: `approve`,
    `approveAlways`, `reject`. Returns `202 Accepted`.
  - Use the request's server-produced permission `pattern`; never rebuild it.
- `POST /api/chats/{id}/elicitation`
  - Body: `{elicitationId, action, content?}`. Actions: `accept`, `decline`,
    `cancel`. Returns `202 Accepted`.
- `POST /api/chats/{id}/retitle`
  - Body: `{title: string}`. Returns `202 Accepted`.
- `POST /api/chats/{id}/compact` → `202 Accepted`
  - Explicitly compacts an idle session.
- `GET /api/chats/{id}/stats` → `200 Stats`
- `DELETE /api/chats/{id}` → `202 Accepted`
  - Closes the live runtime without deleting stored session history.

Unknown `/api/*` routes return `404 {code:"not_found", ...}`.

## Backend wire types

The following are the JSON/TypeScript shapes. Go may encode an absent slice as
`null`; use `?? []` in plugin code.

```ts
type RunState = "idle" | "running" | "stopping";
type DeliveryMode = "normal" | "steer" | "followUp";
type ToolState = "pending" | "awaiting_confirmation" | "running" |
  "success" | "error" | "rejected";
type ToolDecision = "approve" | "approveAlways" | "reject";
type ElicitationAction = "accept" | "decline" | "cancel";

interface Health { status: string; uptimeSeconds: number }
interface APIError { error: string; code: string; details?: string }
interface Notice { id: string; level: "info"|"warning"|"error"; message: string; code: string }
interface WorkspaceHint { path: string; label: string }
interface Bootstrap {
  appVersion: string; agentVersion: string; agentCommit: string;
  configDir: string; dataDir: string; cacheDir: string; sessionDb: string;
  pluginDir: string; csrfToken: string; sandboxed: boolean;
  modelsAvailable: boolean; modelsHint: string;
  workspaceHints: WorkspaceHint[] | null; notices: Notice[] | null;
}
interface Workspace {
  workspaceId: string; path: string; label: string; notices: Notice[] | null;
  agentsMd: boolean; agentsIgnore: boolean;
}
interface PermissionsView {
  allow: string[] | null; ask: string[] | null; deny: string[] | null;
  agentsIgnore: boolean; sessionGrants: string[] | null;
}
interface SessionSummary {
  sessionId: string; title: string; workingDir: string; createdAt: string;
  messages: number; live: boolean; chatId?: string; runState?: RunState;
}
interface ChatRef { chatId: string; sessionId: string }
interface QueueStatus {
  steerDepth: number; steerCapacity: number;
  followUpDepth: number; followUpCapacity: number;
}
interface RunStatus { state: RunState; runId: string; queue: QueueStatus }
interface Usage {
  inputTokens: number; outputTokens: number; cost: number; contextLimit: number;
}
interface SessionMeta {
  chatId: string; sessionId: string; title: string; workspaceId: string;
  workingDir: string; agentName: string; model: string; thinkingLevel: string;
  thinkingLevels: string[] | null; permissions: PermissionsView; createdAt: string;
}
interface MessageItem {
  id: string; role: string; agentName: string; text: string; reasoning: string;
  streaming: boolean; createdAt: string; model: string; cost?: number;
  inputTokens?: number; outputTokens?: number; cachedInputTokens?: number;
  cacheWriteTokens?: number; reasoningTokens?: number;
}
interface ToolImage { name: string; mimeType: string; data: string }
interface ToolActivity {
  id: string; name: string; displayName?: string; category: string;
  agentName: string; argsSummary: string; arguments?: Record<string, unknown>;
  images?: ToolImage[]; state: ToolState; preview: string; truncated: boolean;
  outputBytes: number; isError: boolean;
}
interface Transfer { id: string; fromAgent: string; toAgent: string; switching: boolean }
interface Summary { id: string; text: string; cost: number }
type Item =
  | {kind:"message"; message?:MessageItem}
  | {kind:"tool"; tool?:ToolActivity}
  | {kind:"transfer"; transfer?:Transfer}
  | {kind:"notice"; notice?:Notice}
  | {kind:"summary"; summary?:Summary};
interface RejectionReason { label: string; reason: string }
interface ToolConfirmationRequest {
  toolCallId: string; toolName: string; displayName?: string; agentName: string;
  argsSummary: string; pattern: string; patternLabel: string;
  metadata?: Record<string,string>; rejectionReasons: RejectionReason[] | null;
}
interface ElicitationRequest {
  elicitationId: string; message: string; mode: string; url: string;
  agentName: string; schema?: unknown;
}
interface Snapshot {
  seq: number; meta: SessionMeta; items: Item[] | null; run: RunStatus;
  usage: Usage; pendingConfirmations: ToolConfirmationRequest[] | null;
  pendingElicitations: ElicitationRequest[] | null;
}
interface Accepted {
  accepted: boolean; mode: DeliveryMode; runId: string; queued: boolean;
}
interface ModelOption {
  name: string; ref: string; provider: string; model: string; family: string;
  contextLimit: number; inputCost: number; outputCost: number;
  isCurrent: boolean; isDefault: boolean; isCustom: boolean; isCatalog: boolean;
}
interface CommandInfo { name: string; description: string; kind: string }
interface Stats {
  usage: Usage; messages: number; toolCalls: number; model: string;
  agentName: string; durationSeconds: number;
}
```

### SSE Event union

Every event is `{type, seq, ...one payload}`. Apply events in sequence order.

```ts
type Event =
 | {type:"snapshot"; seq:number; snapshot:Snapshot}
 | {type:"run_status"; seq:number; run:RunStatus}
 | {type:"message_item"; seq:number; message:MessageItem}
 | {type:"assistant_delta"|"reasoning_delta"; seq:number;
    delta:{itemId:string;text:string}}
 | {type:"assistant_end"|"reasoning_end"; seq:number; ref:{itemId:string}}
 | {type:"tool_start"|"tool_update"|"tool_end"; seq:number; tool:ToolActivity}
 | {type:"tool_confirmation"; seq:number; confirmation:ToolConfirmationRequest}
 | {type:"tool_confirmation_resolved"; seq:number;
    toolResolved:{toolCallId:string;decision:ToolDecision;pattern:string}}
 | {type:"elicitation"; seq:number; elicitation:ElicitationRequest}
 | {type:"elicitation_resolved"; seq:number; elicitResolved:{elicitationId:string}}
 | {type:"transfer"; seq:number; transfer:Transfer}
 | {type:"usage"; seq:number; usage:Usage}
 | {type:"notice"; seq:number; notice:Notice}
 | {type:"session_meta"; seq:number; meta:SessionMeta}
 | {type:"gap"; seq:number}
 | {type:"chat_closed"; seq:number; closed:{reason:string}};
```

## Plugin manifest and routing

A plugin directory contains `plugin.json`, a `.js`/`.mjs` entry, optional CSS,
and relative assets. No JSX/TypeScript/npm build or bare imports are available.
Relative module imports work.

```json
{
  "apiVersion": 1,
  "id": "example-plugin",
  "name": "Example plugin",
  "description": "Optional description",
  "version": "1.0.0",
  "entry": "index.js",
  "style": "style.css",
  "pages": [
    {"id":"overview", "path":"", "label":"Example", "sidebar":true},
    {"id":"details", "path":"details", "label":"Details", "sidebar":false}
  ]
}
```

Manifest and directory IDs are identical lowercase kebab-case. Page IDs are
kebab-case; paths are unique lowercase URL paths. At least one page is required.
`sidebar:true` contributes a global sidebar item. The catalog shapes are:

```ts
interface PluginPage { id:string; path:string; label:string; sidebar:boolean }
interface Plugin {
  apiVersion:number; id:string; name:string; description:string; version:string;
  fingerprint:string; entryUrl:string; styleUrl?:string;
  pages:PluginPage[]|null;
}
interface PluginError { pluginId?:string; message:string }
interface PluginCatalog { plugins:Plugin[]|null; errors:PluginError[]|null }
```

The module exports `mount(context)`, synchronously or asynchronously, and may
return a cleanup function. The host aborts `signal`, calls cleanup, removes CSS,
and unmounts roots made by `ui.render` on navigation or hot reload.

```ts
interface PluginContext {
  root: HTMLElement;
  workspace: Workspace | null;
  bootstrap: Bootstrap;
  plugin: Plugin;
  page: PluginPage;
  routePath: string;
  signal: AbortSignal;
  api: DashboardAPI;
  ui: PluginUI;
  navigate(path: string): void;
}
```

## Complete frontend API client

```ts
interface DashboardAPI {
  request<T>(method:string, path:string, body?:unknown,
    options?:{signal?:AbortSignal}): Promise<T>;
  setCsrfToken(token:string): void;
  csrfToken(): string;
  bootstrap(): Promise<Bootstrap>;
  plugins(): Promise<PluginCatalog>;
  openWorkspace(path:string): Promise<Workspace>;
  liveSessions(): Promise<SessionSummary[]>;
  sessions(workspaceId:string): Promise<SessionSummary[]>;
  createChat(workspaceId:string): Promise<ChatRef>;
  resumeChat(workspaceId:string, sessionId:string): Promise<ChatRef>;
  snapshot(chatId:string): Promise<Snapshot>;
  send(chatId:string, text:string, mode:DeliveryMode,
    idempotencyKey:string): Promise<Accepted>;
  abort(chatId:string): Promise<Accepted>;
  updateConfig(chatId:string,
    patch:{model?:string;thinkingLevel?:string}): Promise<SessionMeta>;
  models(chatId:string): Promise<ModelOption[]>;
  commands(chatId:string): Promise<CommandInfo[]>;
  confirmTool(chatId:string, reply:{toolCallId:string;
    decision:ToolDecision;reason:string}): Promise<Accepted>;
  answerElicitation(chatId:string, reply:{elicitationId:string;
    action:ElicitationAction;content?:Record<string,unknown>}): Promise<Accepted>;
  retitle(chatId:string, title:string): Promise<Accepted>;
  compact(chatId:string): Promise<Accepted>;
  stats(chatId:string): Promise<Stats>;
  dispose(chatId:string): Promise<Accepted>;
}
```

## Host React and rendering

Use the host React; never import or bundle another copy. Runtime plugins have no
JSX transform.

```js
export function mount(context) {
  const {React, components, render} = context.ui;
  return render(React.createElement(components.Markdown, null, "# Hello"));
}
```

`ui.render(node, target=context.root)` creates/replaces a React root and returns
an unmount function. All roots it creates are automatically unmounted.

```ts
interface PluginUI {
  React: typeof React;
  components: HostComponents;
  hooks: {useChat, useDraft};
  render(node: ReactNode, target?: HTMLElement): () => void;
}
```

## Complete host component registry

### `components.Chat`

High-level complete embedded chat.

```ts
Chat({chatId: string})
```

It owns SSE reduction, persisted draft, slash-command loading, send/steer/
follow-up behavior, stop, conversation rendering, tool confirmation, and MCP
elicitation dialogs. Obtain a live `chatId` from `createChat` or `resumeChat`.
Closing the plugin does not dispose the backend chat.

### `components.Markdown`

```ts
Markdown({children: string})
```

Safe GFM with math and Mermaid fences. Raw HTML and remote images are disabled;
links permit only safe HTTP/HTTPS/mailto URLs.

### `components.Mermaid`

```ts
Mermaid({code: string})
```

Renders sanitized Mermaid SVG and handles loading/errors.

### `components.Conversation`

```ts
Conversation({items: Item[], empty: ReactNode})
```

Renders messages, reasoning, tool cards, transfers, notices, summaries, live
streaming state, automatic scroll pinning, and “Jump to latest”.

### `components.Composer`

```ts
Composer({
  draft: string;
  onDraftChange(value:string): void;
  run: RunStatus;
  disabled: boolean;
  commands: CommandInfo[];
  onSend(text:string, mode:DeliveryMode): void;
  onStop(): void;
})
```

Provides multiline input, slash completion, queue controls, and keyboard/touch
behavior. The owner performs API calls.

### `components.ChatHeader`

```ts
ChatHeader({
  hasChat:boolean; state:ChatState; connection:ConnectionState;
  models:ModelOption[]; busyAction:boolean;
  menuButton:{current:HTMLButtonElement|null}; drawerOpen:boolean;
  onToggleDrawer():void;
  onPatchConfig(patch:{model?:string;thinkingLevel?:string}):void;
  onCompact():void; onRename(title:string):void;
})
```

`ConnectionState` is `connecting|connected|reconnecting|disconnected`.

### `components.ToolCard`

```ts
ToolCard({tool: ToolActivity})
```

Uses dashboard-native renderers for shell, filesystem, search, edits, images,
and generic tool results.

### `components.ModelPicker`

```ts
ModelPicker({models:ModelOption[], current:string, disabled:boolean,
  onSelect(ref:string):void})
```

Searchable grouped model dialog with keyboard support.

### `components.PendingDialogs`

```ts
PendingDialogs({
  state:ChatState;
  onToolDecision(decision:ToolDecision, reason:string):void;
  onElicitationAnswer(action:ElicitationAction,
    content:Record<string,unknown>):void;
})
```

Shows the first pending confirmation or elicitation.

### `components.ToolConfirmDialog`

```ts
ToolConfirmDialog({request:ToolConfirmationRequest,
  onDecide(decision:ToolDecision, reason:string):void})
```

### `components.ElicitationDialog`

```ts
ElicitationDialog({request:ElicitationRequest,
  onAnswer(action:ElicitationAction, content:Record<string,unknown>):void})
```

## Complete host hook registry

Hooks must run inside a plugin React component rendered by `ui.render`.

### `hooks.useChat(chatId: string | null)`

Returns:

```ts
{
  state: ChatState;
  connection: "connecting"|"connected"|"reconnecting"|"disconnected";
  resnapshot(): Promise<void>;
  setState: React state setter for ChatState;
}
```

`ChatState` is:

```ts
interface ChatState {
  seq:number; items:Item[]; meta:SessionMeta|null; run:RunStatus; usage:Usage;
  confirmations:ToolConfirmationRequest[]; elicitations:ElicitationRequest[];
  closed:boolean; closedReason:string;
}
```

The hook owns SSE reconnect, replay sequence tracking, animation-frame batching,
and event reduction. It does not send messages.

### `hooks.useDraft(sessionId: string | null)`

Returns `{draft:string, setDraft(text:string):void}` and persists a separate
browser-local draft for each stable session ID.

## Plugin implementation rules

- Prefer `components.Chat` and other host components over copying core UI.
- Use `React.createElement`; there is no JSX transform.
- Use relative ESM imports only. Fingerprinted URLs preserve relative imports.
- Use `textContent` for untrusted text in imperative DOM code.
- Pass `context.signal` into generic API requests and clean up timers, event
  listeners, observers, and direct `EventSource` instances.
- Prefix CSS selectors with the plugin ID. Plugin CSS is document-global while
  mounted.
- The plugin is trusted and same-origin: it can access the DOM, API, CSRF token,
  and all data exposed to the dashboard.
