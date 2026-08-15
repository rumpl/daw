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
- IDs beginning with `ws_` and `chat_` are process-local opaque IDs.
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
  - Lists plugins that are currently running plus validation diagnostics.
- `GET /api/plugin-management` → `200 PluginManagementCatalog`
  - Lists every installed valid plugin, including stopped and disabled plugins.
- `POST /api/plugins/{pluginId}/start` → `200 ManagedPlugin`
- `POST /api/plugins/{pluginId}/stop` → `200 ManagedPlugin`
- `POST /api/plugins/{pluginId}/enable` → `200 ManagedPlugin`
- `POST /api/plugins/{pluginId}/disable` → `200 ManagedPlugin`
  - Start and stop change the current process state. Disable persists across
    dashboard restarts and also stops the plugin; enable does not start it.
- `DELETE /api/plugins/{pluginId}` → `200 Accepted`
  - Stops the plugin and permanently removes its directory from disk.
- `GET|POST|PUT|PATCH|DELETE /api/plugins/{pluginId}/backend[/{path...}]`
  - Proxies to the plugin's optional Node backend with the prefix removed.
  - Browser mutations use the normal CSRF rules through `context.api`.
- `GET /api/plugins/{pluginId}/events` → plugin-owned SSE stream
  - Reconnects with `lastEventId`; carries `{type,seq,data?}` events published
    by that plugin's backend.
- `POST /api/plugins/{pluginId}/execution-locations`
  - Backend-only registration of a short-lived, single-use execution directory
    capability. Body: `{workspaceId, workingDir, ttlSeconds?}`. Plugin backends
    should call the injected `registerExecutionLocation` helper.
- `GET /api/plugins/{pluginId}/config` → `PluginConfiguration`
- `PUT /api/plugins/{pluginId}/config` → `PluginConfiguration`
  - Reads or replaces the plugin's host-managed public JSON configuration.
- `POST /api/plugins/{pluginId}/publish`
  - Backend-authenticated event publication; not available to browser plugins.
- `GET /api/plugins/{pluginId}/webhooks/{webhookId}/token`
  - Backend-only retrieval of generated webhook URL and Bearer token.
- `* /api/plugins/{pluginId}/webhooks/{webhookId}`
  - External webhook route; bypasses browser CSRF but requires its generated
    Bearer token and a manifest-declared webhook ID.
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
- `GET /api/workspaces/{workspaceId}/sessions/{sessionId}` → `200 StoredSession`
  - Read-only persisted metadata, usage, and aggregate stats. It never opens a
    live chat or starts agent resources.
- `GET /api/workspaces/{workspaceId}/sessions/{sessionId}/items[?offset=N&limit=N]` → `200 StoredSessionItems`
  - A normalized persisted timeline page. `offset` defaults to 0; `limit`
    defaults to 200 and is capped at 1000.
- `POST /api/chats`
  - Body: `{workspaceId: string, executionLocationId?: string}`. A trusted
    plugin backend can issue the opaque location ID to run the session in a
    different directory while keeping it grouped under the logical workspace.
    Returns `201 ChatRef`.
- `POST /api/chats/resume`
  - Body: `{workspaceId: string, sessionId: string}`.
  - Returns `201 ChatRef` when opening a stored session, or `200 ChatRef` when
    attaching to its already-live runtime. A session can have only one live
    runtime in this server.
- `GET /api/chat-options` → `200 ChatOptions`
  - Returns the process-wide model and tool catalogs, supported thinking levels,
    and the defaults inherited by new chats. It does not create a chat or session.
- `PATCH /api/chat-options` → `200 ChatOptions`
  - Body: `{model?: string, thinkingLevel?: string}`. Updates global defaults;
    an explicit empty string clears a preference. No chat is required.
- `PATCH /api/chat-options/tools/{tool}` → `200 ToolOption`
  - Body: `{enabled: boolean}`. Updates whether the named tool is offered by
    chats opened or resumed after the change.
- `GET /api/chats/{id}` → `200 Snapshot`
  - Complete authoritative chat state for resnapshot/reconciliation.
- `GET /api/chats/{id}/events[?lastEventId=N]` → SSE stream
  - Each `data:` frame is one `Event`. Event `seq` is monotonic per live chat.
    Reconnect with the last applied sequence. A replay gap produces a fresh
    snapshot/gap recovery. The server sends heartbeat comments.
- `POST /api/chats/{id}/attachments`
  - Multipart form with one `file` field. Returns `201 Attachment`. Files are
    limited to 10 MB; text, PDF, JPEG, PNG, GIF, and WebP are accepted. Up to
    four unsent uploads may be pending per chat.
- `DELETE /api/chats/{id}/attachments/{attachmentId}` → `204`
  - Discards an uploaded attachment before it is sent.
- `POST /api/chats/{id}/messages`
  - Body: `{text, mode, attachments?: string[]}` where attachments contains
    opaque IDs returned by the upload endpoint.
  - The server dispatches from authoritative run state: idle messages start a
    turn; while running, `followUp` queues a later turn and other messages steer
    the current turn. A stale in-run mode received after settlement starts a
    normal turn. Returns `202 Accepted` with the mode actually applied.
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
  - Body: `{title: string}`. Returns `200 Accepted`.
- `POST /api/chats/{id}/compact` → `202 Accepted`
  - Explicitly compacts an idle session.
- `GET /api/chats/{id}/stats` → `200 Stats`
- `DELETE /api/chats/{id}` → `200 Accepted`
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
  messages: number; cost?: number; live: boolean; chatId?: string; runState?: RunState;
  parentSessionId?: string; rootSessionId?: string; originKind?: string;
  originPluginId?: string;
}
interface StoredSessionMeta {
  sessionId:string; title:string; workspaceId:string; workingDir:string;
  agentName:string; model:string; createdAt:string; parentSessionId?:string;
  rootSessionId?:string; originKind?:string; originPluginId?:string;
}
interface StoredSession {
  meta:StoredSessionMeta; usage:Usage; stats:Stats; live:boolean;
}
interface StoredSessionItems {
  items:Item[]|null; offset:number; limit:number; total:number; nextOffset?:number;
}
interface ChatRef { chatId: string; sessionId: string }
interface QueuedMessage { id: string; text: string }
interface QueueStatus {
  steerDepth: number; steerCapacity: number;
  followUpDepth: number; followUpCapacity: number;
  steer: QueuedMessage[] | null; followUps: QueuedMessage[] | null;
}
interface RunStatus { state: RunState; runId: string; queue: QueueStatus }
interface Usage {
  inputTokens: number; outputTokens: number; cost: number; contextLimit: number;
}
interface SessionMeta {
  chatId: string; sessionId: string; title: string; workspaceId: string;
  workingDir: string; agentName: string; model: string; thinkingLevel: string;
  thinkingLevels: string[] | null; permissions: PermissionsView; createdAt: string;
  parentSessionId?: string; rootSessionId?: string; originKind?: string;
  originPluginId?: string;
}
interface Attachment { id: string; name: string; mimeType: string; size: number; data?: string }
interface MessageItem {
  id: string; role: string; agentName: string; text: string; reasoning: string;
  streaming: boolean; createdAt: string; model: string; attachments?: Attachment[]; cost?: number;
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
  accepted: boolean; mode: DeliveryMode | ""; runId: string; queued: boolean;
}
interface ModelOption {
  name: string; ref: string; provider: string; model: string; family: string;
  contextLimit: number; inputCost: number; outputCost: number;
  isCurrent: boolean; isDefault: boolean; isCustom: boolean; isCatalog: boolean;
}
interface ChatOptions {
  model: string; thinkingLevel: string;
  thinkingLevels: string[] | null; models: ModelOption[] | null;
  tools: ToolOption[] | null;
}
interface CommandInfo { name: string; description: string; kind: string }
interface Stats {
  usage: Usage; messages: number; toolCalls: number; model: string;
  agentName: string; durationSeconds: number;
}
```

`mode` and `runId` are populated for accepted message delivery. Other
operations returning `Accepted` currently encode `mode:""`, `runId:""`, and
`queued:false`.

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
 | {type:"summary"; seq:number; summary:Summary}
 | {type:"session_meta"; seq:number; meta:SessionMeta}
 | {type:"gap"; seq:number}
 | {type:"chat_closed"; seq:number; closed:{reason:string}};
```

### Dashboard SSE Event union

The dashboard-wide stream uses the same monotonically increasing sequence and
replay rules:

```ts
type DashboardEvent =
 | {type:"snapshot"; seq:number}
 | {type:"sessions_changed"; seq:number; workspaceIds?:string[];
    sessionIds?:string[]; reason?:string}
 | {type:"plugins_changed"; seq:number; revision?:string}
 | {type:"gap"; seq:number};
```

Payload fields are optional on the wire. A `gap` means the requested replay
point is unavailable; refresh authoritative REST resources.

## Plugin manifest and routing

A plugin directory contains `plugin.json`, a `.js`/`.mjs` frontend entry,
optional CSS and relative assets, and may contain a Node backend JavaScript
project. Frontend modules have no JSX/TypeScript/npm build or bare imports.
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
  "backend": {"entry": "backend/index.js", "webhooks":[{"id":"github"}],
    "mcp":[{"id":"tools","command":"node","args":["server.mjs"]}]},
  "configuration": {"type":"object"},
  "pages": [
    {"id":"overview", "path":"", "label":"Example", "sidebar":true},
    {"id":"details", "path":"details", "label":"Details", "sidebar":false}
  ]
}
```

Manifest and directory IDs are identical lowercase kebab-case and at most 63
characters. Page IDs follow the same form. Page paths are unique lowercase URL
paths; leading and trailing slashes are normalized, but empty components and
`//` are rejected. A plugin may declare up to 30 pages, with unique page IDs
and paths. Page-less plugins can provide global contributions from `activate`.
`sidebar:true` contributes a global sidebar item.

The manifest is a single JSON object no larger than 64 KiB and unknown fields
are rejected. `name` is required and at most 80 characters; page labels are
required and at most 60 characters; `description` is at most 300 characters;
`version` is at most 40 characters. `entry` must be a relative `.js` or `.mjs`
path and optional `style` must be a relative `.css` path. When declared, both
must name regular files within the plugin.

`entry`, `description`, `version`, `style`, `backend`, `configuration`, and
`pages` are optional, but at least one of `entry` or `backend` is required. A
backend entry
must be a relative `.js`, `.mjs`, or `.cjs` path inside a backend directory.
The backend directory is a normal Node project and may use npm dependencies.
It is excluded from browser assets and frontend size limits.

A backend may declare up to 20 MCP servers under `backend.mcp`. Each entry has a
unique `id` and exactly one of:

- local stdio: `command`, optional `args`, `env`, and workspace-relative
  `workingDir`
- remote: HTTP(S) `url`, optional `transport` (`sse`, `streamable`, or
  `streamable-http`) and `headers`

They are namespaced `<plugin-id>-<server-id>` and included in the global tool
catalog and enabled filter. Each chat runtime creates an independent native MCP
transport from that shared configuration. Existing transports retain their
opened graph after a plugin edit; reopening uses the latest manifest. Trusted
local MCP processes
receive the owning plugin's process-private `@daw/plugin-backend` API transport
and credentials, so they can call `/api/plugins/${pluginId}/backend/...`; the
SDK selects loopback HTTP in web mode and UDS in Electron. Remote MCP servers
never receive these credentials. Local MCP processes receive `DAW_CHAT_ID` and
`DAW_SESSION_CONTEXT` when each runtime creates its transport. Backends and local MCP
processes inherit `DAW_INSTANCE_ID`; any unavoidable private socket, lock, or
other IPC resource must include it so concurrent web and Electron instances do
not collide, though the authenticated backend route is preferred.

Backend entries may export `activate(context)`, `default`/`handler(request,
context)`, and `webhook(request, context)`. Activation may return an async
cleanup function. The dashboard starts backends eagerly, restarts them on source
changes, and proxies `/api/plugins/{pluginId}/backend/{path...}` with the
backend prefix removed. The plugin catalog exposes `backendUrl` when present.
Backend code can import the injected `@daw/plugin-backend` package:

```js
import { dashboard } from "@daw/plugin-backend";
export default async function handler(request) {
  const health = await dashboard.request("GET", "/api/health");
  return Response.json(health);
}
```

`dashboard.request(method, path, body?, options?)` parses JSON, authenticates
mutations, and throws `DashboardApiError`; `dashboard.raw(...)` returns the raw
fetch `Response`. A chat-scoped MCP process may propagate its opaque
`DAW_SESSION_CONTEXT` through `options.sessionContext`; the SDK transports it
as an authenticated header and chat creation records durable provenance.
Request and response bodies stream with cancellation. The SDK also exports:

- `registerExecutionLocation(workspaceId, workingDir, {ttlSeconds?})`
  registers a backend-selected directory and returns
  `{executionLocationId, expiresAt}`. Capabilities are single-use and expire
  after five minutes by default.
- `storage.get/set/delete(key)` for backend-only namespaced JSON storage
- `configuration.get/set()` for host-managed public configuration
- `events.subscribeDashboard()` and `events.publish()`
- `webhooks.credentials(id)` for a declared webhook's URL and Bearer token

Backend logic can support frontend commands through the existing namespaced
HTTP handler; the frontend registration callback calls that handler with
`context.api`.

Never expose the injected API credential or webhook tokens to ordinary browser
responses.

Plugin symlinks and special filesystem entries are not supported; nested
regular directories and files are allowed. A plugin frontend may contain at most 200
files, each at most 4 MiB, with a maximum total size of 16 MiB. The catalog
shapes are:

```ts
interface PluginPage { id:string; path:string; label:string; sidebar:boolean }
interface PluginMCPServer {id:string; transport:string}
interface PluginFeatures {
  frontend:boolean; styles:boolean; backend:boolean; configuration:boolean;
  webhooks:string[]|null; mcpServers:PluginMCPServer[]|null;
}
interface Plugin {
  apiVersion:number; id:string; name:string; description:string; version:string;
  fingerprint:string; entryUrl:string; styleUrl?:string; backendUrl?:string;
  eventsUrl?:string; configUrl?:string; configuration?:unknown;
  features?:PluginFeatures; pages:PluginPage[]|null;
}
interface PluginEvent {type:string;seq:number;data?:unknown}
interface PluginConfiguration {values:Record<string,unknown>|null}
interface PluginError { pluginId?:string; message:string }
interface ManagedPlugin { plugin:Plugin; enabled:boolean; running:boolean }
interface PluginManagementCatalog { plugins:ManagedPlugin[]|null; errors:PluginError[]|null }
interface PluginCatalog { plugins:Plugin[]|null; errors:PluginError[]|null }
```

The module may export `activate(context)` for global contributions and/or
`mount(context)` for declared pages. Both may be synchronous or asynchronous
and may return a cleanup function. Activation is independent of page navigation
and restarts after plugin or active-workspace changes. The host aborts `signal`,
calls cleanup, removes contributions and CSS, and unmounts roots made by
`ui.render` on deactivation or hot reload.

```ts
interface ContributionContext {
  workspace: Workspace|null; chatId:string|null; session:SessionMeta|null;
  sessionId?:string; message?:MessageItem;
}
interface PluginAction {
  id:string; label:string; description?:string;
  locations:("command-palette"|"composer")[];
  when?(context:ContributionContext):boolean;
  run(context:ContributionContext):void|Promise<void>;
}
interface SlotContribution {
  id:string;
  slot:"assistant-message.actions"|"composer.actions"|"session-tab.badge"|"sidebar.footer";
  order?:number;
  render(context:ContributionContext):ReactNode;
}
interface ToolActionContribution {
  id:string; label:string; icon?:ReactNode; description?:string;
  match(tool:ToolActivity, context:ContributionContext):boolean;
  run(tool:ToolActivity, context:ContributionContext):void|Promise<void>;
}
interface SessionSideViewOptions {
  id:string; sessionId:string; title:string;
  render(context:ContributionContext & {close():void}):ReactNode;
}
interface PluginContributions {
  registerAction(action:PluginAction):()=>void;
  registerSlot(contribution:SlotContribution):()=>void;
  registerCommand(command:{id:string;name:string;description:string;
    run(text:string,context:ContributionContext):string|void|Promise<string|void>}):()=>void;
  registerToolRenderer(renderer:{id:string;match(tool:ToolActivity):boolean;
    render(tool:ToolActivity):ReactNode}):()=>void;
  registerToolAction(action:ToolActionContribution):()=>void;
  openSessionSideView(options:SessionSideViewOptions):()=>void;
  registerAttachmentRenderer(renderer:{id:string;match(attachment:Attachment):boolean;
    render(attachment:Attachment):ReactNode}):()=>void;
  setSessionBadge(sessionId:string, badge:{id:string;value:string;
    tone?:"info"|"warning"|"error"|"success"}):()=>void;
  notify(notification:{id:string;level:"info"|"warning"|"error";
    title:string;message?:string;timeoutMs?:number}):()=>void;
}
interface PluginEvents {
  subscribeDashboard(options:{types?:DashboardEvent["type"][]},
    listener:(event:DashboardEvent)=>void):()=>void;
  subscribePlugin(pluginId:string, options:{types?:string[]},
    listener:(event:PluginEvent)=>void):()=>void;
  subscribeChat(chatId:string, options:{types?:Event["type"][]},
    listener:(event:Event)=>void):()=>void;
}
interface PluginActivationContext {
  workspace:Workspace|null; bootstrap:Bootstrap; plugin:Plugin;
  signal:AbortSignal; api:DashboardAPI; ui:PluginUI;
  contributions:PluginContributions; events:PluginEvents;
}
```

Managed event subscriptions reconnect with replay positions and are closed on
deactivation. Plugin commands participate in slash completion; returning text
supplies the prompt and returning undefined handles the action without sending.
Matching tool and attachment renderers replace the host fallback under a plugin
error boundary. Matching tool actions add buttons beside the host tool card;
they run only when the user clicks the button. `openSessionSideView`
opens or replaces the contextual view beside that session and returns an
idempotent close function. Views are scoped by stable session ID, survive tab
switching, and are removed when their plugin stops or the live session closes.
On narrow screens the view overlays the chat. The `assistant-message.actions` slot is rendered beside the
“Download as Markdown” button on each completed assistant message and receives
that message as `context.message`; other slot contexts omit `message`. The
command palette opens with Cmd/Ctrl+K. Notification
`timeoutMs` defaults to 6000; zero keeps it visible. Global plugin CSS remains
loaded for the activation lifetime.

### Future consideration: context providers

Plugin-provided model context is intentionally excluded from API v1 for now. It
may be reconsidered with explicit user opt-in, provenance, inspection, size
limits, and per-send controls. Current plugins must not silently append context
to user messages.

Page mounting uses this context:

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
  pluginConfiguration(pluginId:string):Promise<PluginConfiguration>;
  updatePluginConfiguration(pluginId:string,
    values:Record<string,unknown>):Promise<PluginConfiguration>;
  openWorkspace(path:string): Promise<Workspace>;
  liveSessions(): Promise<SessionSummary[]>;
  sessions(workspaceId:string): Promise<SessionSummary[]>;
  session(workspaceId:string, sessionId:string,
    options?:{signal?:AbortSignal}):Promise<StoredSession>;
  sessionItems(workspaceId:string, sessionId:string,
    options?:{offset?:number;limit?:number;signal?:AbortSignal}):Promise<StoredSessionItems>;
  chatOptions(): Promise<ChatOptions>;
  updateChatOptions(patch:{model?:string;thinkingLevel?:string}):Promise<ChatOptions>;
  updateDefaultTool(name:string, enabled:boolean):Promise<ToolOption>;
  createChat(workspaceId:string, executionLocationId?:string): Promise<ChatRef>;
  resumeChat(workspaceId:string, sessionId:string): Promise<ChatRef>;
  snapshot(chatId:string): Promise<Snapshot>;
  uploadAttachment(chatId:string, file:File,
    options?:{signal?:AbortSignal}): Promise<Attachment>;
  deleteAttachment(chatId:string, attachmentId:string): Promise<void>;
  send(chatId:string, text:string, mode:DeliveryMode,
    attachments?:string[]): Promise<Accepted>;
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
Conversation({items: Item[], queue?: QueueStatus, empty: ReactNode,
  contributionContext?: ContributionContext})
```

Renders messages, reasoning, tool cards, transfers, notices, summaries, live
streaming state, optional queued steer/follow-up messages, automatic scroll
pinning, and “Jump to latest”. Pass `contributionContext` to expose registered
assistant-message actions; the complete message is added as `context.message`.

### `components.Composer`

```ts
Composer({
  draft: string;
  onDraftChange(value:string): void;
  run: RunStatus;
  disabled: boolean;
  commands: CommandInfo[];
  attachments: Attachment[];
  uploading: boolean;
  onAddAttachments(files:File[]): void;
  onRemoveAttachment(id:string): void;
  onSend(text:string, mode:DeliveryMode): void;
  onStop(): void;
})
```

Provides multiline input, slash completion, file selection, paste and drag/drop,
queue controls, and keyboard/touch behavior. The owner performs API calls.

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

- Plugins without pages should export `activate`; page plugins export `mount`.
- Prefer `components.Chat` and other host components over copying core UI.
- Use `React.createElement`; there is no JSX transform.
- Use relative ESM imports only. Fingerprinted URLs preserve relative imports.
- Use `textContent` for untrusted text in imperative DOM code.
- Pass `context.signal` into generic API requests and clean up timers, event
  listeners, observers, and direct `EventSource` instances.
- Prefix CSS selectors with the plugin ID. Plugin CSS is document-global while
  the plugin is activated.
- The plugin is trusted and same-origin: it can access the DOM, API, CSRF token,
  and all data exposed to the dashboard.
