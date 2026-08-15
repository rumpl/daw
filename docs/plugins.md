# Dashboard plugins

Dashboard plugins are trusted, global browser modules. They are discovered at
startup and every few seconds while the dashboard is open. The SDK-built
`dashboard-coder` can retrieve the complete backend and frontend contract at any
time with its read-only `get_dashboard_developer_documentation` tool.

The plugin directory is `DAWUI_PLUGIN_DIR`, defaulting to:

```text
~/.cagent/dawui/plugins
```

Each direct child is one plugin. Its directory name and manifest `id` must be
the same lowercase kebab-case value.

```text
~/.cagent/dawui/plugins/project-health/
├── plugin.json
├── index.js
├── style.css
└── backend/
    ├── package.json
    └── index.js
```

## Manifest

```json
{
  "apiVersion": 1,
  "id": "project-health",
  "name": "Project health",
  "description": "Shows repository and backend status",
  "version": "1.0.0",
  "entry": "index.js",
  "style": "style.css",
  "backend": {
    "entry": "backend/index.js",
    "webhooks": [{"id":"github"}],
    "mcp": [
      {"id":"local-tools","command":"node","args":["server.mjs"],"workingDir":"tools"},
      {"id":"remote-tools","url":"https://mcp.example.test","transport":"streamable-http"}
    ]
  },
  "configuration": {
    "type": "object",
    "properties": {"enabled":{"type":"boolean"}}
  },
  "pages": [
    {
      "id": "overview",
      "path": "",
      "label": "Project health",
      "sidebar": true
    },
    {
      "id": "details",
      "path": "details",
      "label": "Health details",
      "sidebar": false
    }
  ]
}
```

`entry`, `description`, `version`, `style`, `backend`, `configuration`, and
`pages` are optional, but at least one of `entry` or `backend` is required.
A plugin can navigate to any declared page with `context.navigate(path)`.

Plugin assets are fingerprinted from their contents. Relative ES module imports
and relative asset URLs therefore receive the same immutable fingerprint. An
edit changes the fingerprint and causes an open plugin page to remount. The
backend directory is private and is never served as a browser asset.

## Node backend

Set `backend.entry` to a `.js`, `.mjs`, or `.cjs` file inside a dedicated
backend directory. The directory is a normal JavaScript project: it may contain
a `package.json`, lockfile, and `node_modules`; run `npm install` there yourself
when dependencies are needed. The dashboard runs the entry with the `node`
executable and restarts it after backend source changes.

The entry exports `default` or `handler`, an async Fetch-style handler, and may
also export lifecycle and webhook handlers. Requests
to `/api/plugins/<id>/backend/...` are proxied to it with the prefix removed:

```js
import { dashboard, pluginId } from "@daw/plugin-backend";

export default async function handler(request) {
  const url = new URL(request.url);
  if (request.method === "GET" && url.pathname === "/health") {
    const health = await dashboard.request("GET", "/api/health");
    return Response.json({ pluginId, dashboard: health.status });
  }
  return Response.json({ error: "not found" }, { status: 404 });
}
```

The dashboard injects `@daw/plugin-backend` into the backend's local
`node_modules`. Its `dashboard.request(method, path, body?, options?)` method
calls dashboard APIs, adds the internal CSRF credential to mutations, parses
JSON, and throws `DashboardApiError` for non-2xx responses. Use
`dashboard.raw(...)` when streaming, multipart, or direct `Response` access is
needed. The credential is process-private and must never be returned to the
browser or logged.

Frontend code calls its backend through the regular plugin API client:

```js
const result = await context.api.request(
  "GET", `${context.plugin.backendUrl}/health`, undefined,
  { signal: context.signal }
);
```

Backend processes bind only to an ephemeral loopback port, start eagerly so
`activate(context)` can observe dashboard events, restart after source changes,
and stop with the dashboard. They are trusted and run with the same user
permissions as the dashboard.

### Backend lifecycle, storage, configuration, and events

A backend entry may export `activate(context)` in addition to its request
handler. Activation completes before the backend begins serving and may return
an async cleanup function:

```js
import { storage, configuration, events } from "@daw/plugin-backend";

export async function activate() {
  const count = (await storage.get("starts")) ?? 0;
  await storage.set("starts", count + 1);
  const stop = events.subscribeDashboard(event => {
    if (event.type === "sessions_changed") {
      void events.publish("sessions-refreshed", { reason: event.reason });
    }
  });
  return stop;
}
```

`storage.get/set/delete(key)` is backend-only namespaced JSON storage. Keys are
simple names and each value is limited to 256 KiB. `configuration.get/set()`
reads and replaces the plugin's host-managed public configuration. Frontends
can use `api.pluginConfiguration(pluginId)` and
`api.updatePluginConfiguration(pluginId, values)`.

`events.subscribeDashboard(listener, {types?})` provides process-local,
at-most-once observation with reconnect/replay. `events.publish(type, data)`
publishes to the plugin's frontend stream. Frontends subscribe with
`context.events.subscribePlugin(pluginId, {types?}, listener)`.

The backend may implement command work behind its existing namespaced HTTP
handler; frontend contribution callbacks invoke it through `context.api`.

### Streaming

Backend request bodies and `Response.body` are piped instead of buffered.
Streaming downloads and SSE therefore work, and browser disconnects abort the
backend `Request.signal`.

### External webhooks

Declare up to 20 authenticated webhook IDs under `backend.webhooks`, then export
`webhook(request, {pluginId, webhookId})`. Retrieve its generated credentials
only from backend code:

```js
import { webhooks } from "@daw/plugin-backend";
const {url, token} = await webhooks.credentials("github");
```

External callers send `Authorization: Bearer <token>` to the returned URL.
Webhook calls bypass browser CSRF/origin checks but require the constant-time
Bearer-token check and are forwarded only to a declared webhook.

## MCP tools

Plugins contribute agent tools through native MCP servers declared under
`backend.mcp`. Each server must choose exactly one transport:

- `command` with optional `args`, `env`, and workspace-relative `workingDir`
- `url` with optional `transport` and `headers`

Server names are namespaced as `<plugin-id>-<server-id>`. MCP server declarations
are part of the global tool catalog and use the same global enabled filter as
built-in tools. Each chat runtime creates its own MCP transport from that shared
configuration, so prompts, elicitation, sampling, tool change notifications,
restart supervision, and shutdown still use docker-agent's native MCP runtime.
Editing a plugin updates the global catalog; existing transports retain the graph
they opened with until their runtime is reopened.

Environment and header values are trusted operator configuration and are never
returned by the plugin catalog. Remote URLs must use HTTP(S), and local working
directories cannot escape the active workspace.

Trusted local MCP processes receive the same injected `@daw/plugin-backend`
transport and credentials as their owning backend. They can import `dashboard`
and `pluginId` and call `/api/plugins/${pluginId}/backend/...`; the SDK uses the
loopback HTTP origin in web mode and the dashboard UDS in Electron automatically.
They also receive `DAW_CHAT_ID` and `DAW_SESSION_CONTEXT` when each runtime
creates its transport. Credentials remain process-private and must never be
returned as tool output or logged. Remote MCP servers never receive these local credentials.

Every plugin backend and local MCP process also inherits `DAW_INSTANCE_ID`, a
short identifier unique to the running dashboard process. Prefer the authenticated
backend route above; if a plugin still needs private sockets, locks, or other IPC
resources, it must include this value in the resource name so concurrent web and
Electron dashboard instances cannot collide.

## Global activation and contributions

A plugin may export `activate(context)`. It runs while the plugin is installed,
independently of whether one of its pages is open, and may return a cleanup
function. Activation restarts when the plugin fingerprint or active workspace
changes.

```js
export function activate(context) {
  const removeAction = context.contributions.registerAction({
    id: "project-health",
    label: "Check project health",
    description: "Run the project health check",
    locations: ["command-palette"],
    run({ workspace }) {
      context.contributions.notify({
        id: "health-result",
        level: "info",
        title: workspace ? `${workspace.label} is healthy` : "No project open"
      });
    }
  });

  const removeFooter = context.contributions.registerSlot({
    id: "health-footer",
    slot: "sidebar.footer",
    render() {
      return context.ui.React.createElement("span", null, "Health checks enabled");
    }
  });

  return () => {
    removeAction();
    removeFooter();
  };
}
```

Actions may be placed in `command-palette` and `composer`.
The command palette opens with Cmd/Ctrl+K. Additive slots are
`assistant-message.actions`, `composer.actions`, `session-tab.badge`, and
`sidebar.footer`. Slot render functions receive
`{workspace, chatId, session, sessionId?, message?}` and must return a host-React
node. The `assistant-message.actions` slot appears beside “Download as Markdown”
on each completed assistant message; its context includes that `message`.

`setSessionBadge(sessionId, {id, value, tone?})` adds a tab badge. Supported
tones are `info`, `warning`, `error`, and `success`.
`notify({id, level, title, message?, timeoutMs?})` shows a host notification;
`timeoutMs: 0` makes it persistent. All registrations and notifications are
removed automatically when activation stops.

Plugin styles are loaded for the full activation lifetime, so global
contributions can use them even while no plugin page is open. Prefix all
selectors with a plugin-specific class to avoid changing core UI.

Activation context also exposes managed events:

```js
const unsubscribe = context.events.subscribeDashboard(
  { types: ["sessions_changed"] },
  event => console.debug(event.reason)
);
const unsubscribeChat = context.events.subscribeChat(
  chatId,
  { types: ["tool_end", "run_status"] },
  event => console.debug(event.type)
);
```

Managed streams reconnect with replay positions and close automatically on
plugin deactivation.

Plugins can also register richer chat integrations during frontend activation:

```js
context.contributions.registerCommand({
  id: "review", name: "acme-review", description: "Review with Acme policy",
  run(args) { return `Review ${args} using Acme policy`; }
});
context.contributions.registerToolRenderer({
  id: "plan", match: tool => tool.name === "terraform_plan",
  render: tool => context.ui.React.createElement("pre", null, tool.preview)
});
context.contributions.registerAttachmentRenderer({
  id: "junit", match: attachment => attachment.mimeType === "application/junit+xml",
  render: attachment => context.ui.React.createElement("strong", null, attachment.name)
});
```

Plugin commands appear in slash completion. Their result becomes the outgoing
prompt; returning `undefined` means the command handled the action itself. A
matching custom renderer replaces the host's normal tool or attachment
presentation, with host error isolation.

A plugin can add an action button to matching tool cards and open a view
beside the session after the user clicks it:

```js
context.contributions.registerToolAction({
  id: "preview-result",
  label: "Preview",
  // Optional `icon` accepts a host-React node; `label` remains the accessible name.
  match: (tool, session) => tool.name === "write_file" && Boolean(session.sessionId),
  run(tool, session) {
    context.contributions.openSessionSideView({
      id: "preview",
      sessionId: session.sessionId,
      title: String(tool.arguments?.path ?? "Preview"),
      render: ({ close }) => context.ui.React.createElement("button", { onClick: close }, "Close")
    });
  }
});
```

`openSessionSideView` replaces the currently visible side view for that session
and returns an idempotent close function. Views follow their stable session ID,
are restored when switching back to the session, and are removed when the
plugin stops or the live session closes. On narrow screens the view overlays
the chat. Tool actions are displayed on matching host tool cards and only run
after the user clicks their button.

### Future consideration: context providers

Plugin-provided model context is intentionally not part of API v1 for now. It
may be revisited later with explicit user opt-in, clear provenance, inspection,
size limits, and per-send controls; plugins must not silently append context to
messages in the current contract.

## Entry module

The entry is a browser-native ES module when `entry` is declared. It may export `activate(context)` for
global contributions and/or `mount(context)` for declared pages. Page plugins
must export `mount(context)`, which may return a cleanup function.

```js
export async function mount(context) {
  const health = await context.api.request("GET", "/api/health", undefined, {
    signal: context.signal
  });

  const wrapper = document.createElement("section");
  wrapper.className = "project-health-page";
  const heading = document.createElement("h1");
  heading.textContent = `Backend: ${health.status}`;
  wrapper.append(heading);
  context.root.append(wrapper);

  const timer = setInterval(() => console.debug("plugin is alive"), 10000);
  return () => clearInterval(timer);
}
```

The context contains:

| Field | Meaning |
| --- | --- |
| `root` | Element owned by the plugin |
| `workspace` | Active workspace or `null` |
| `bootstrap` | Dashboard bootstrap response |
| `plugin` | Plugin descriptor |
| `page` | Active manifest page |
| `routePath` | Active plugin route |
| `signal` | Aborted when the plugin is unmounted or reloaded |
| `navigate(path)` | Navigate to another declared plugin page |
| `api` | The complete dashboard API client |
| `ui` | Host React, components, hooks, and managed rendering |

`api.request(method, path, body?, options?)` is the generic escape hatch for all
backend endpoints. It serializes JSON, adds the dashboard CSRF header to
mutations, and throws the same `ApiError` used by core UI. Named methods such as
`api.sessions`, `api.chatOptions`, `api.updateChatOptions`, `api.createChat`,
`api.snapshot`, and `api.send` are also available. Same-origin GET event streams
can be opened with `EventSource`.

## Host components

Plugins should reuse the host's React instance rather than bundle React. The UI
registry currently exposes:

- `Chat`, a complete embeddable chat accepting `{ chatId }`
- `Markdown` and `Mermaid`
- `Conversation`, `Composer`, and `ChatHeader`
- `PendingDialogs`, `ToolConfirmDialog`, and `ElicitationDialog`
- `ModelPicker` and `ToolCard`
- hooks `useChat(chatId)` and `useDraft(sessionId)`

There is no JSX transform in the runtime plugin loader. Compose host components
with `React.createElement`:

```js
export function mount(context) {
  const { React, components, render } = context.ui;
  return render(
    React.createElement(components.Markdown, null, "# Rendered by the dashboard")
  );
}
```

To embed a complete existing chat, render `components.Chat` with its opaque
`chatId`. It includes streaming, the conversation, composer, slash commands,
stop behavior, tool confirmation, and elicitation dialogs.

A plugin can also define a component that uses host hooks:

```js
export function mount(context) {
  const { React, components, hooks, render } = context.ui;

  function LiveConversation({ chatId }) {
    const { state } = hooks.useChat(chatId);
    return React.createElement(components.Conversation, {
      items: state.items,
      empty: React.createElement("p", null, "No messages")
    });
  }

  return render(React.createElement(LiveConversation, { chatId: "chat_..." }));
}
```

Roots created by `ui.render` are automatically unmounted. Its returned cleanup
function can still be returned from `mount` for explicit cleanup.

These are versioned by `apiVersion`; their props follow the dashboard's current
TypeScript components. Prefer the higher-level components over duplicating
chat rendering and tool presentation.

## Constraints

- Use `.js` or `.mjs`, not TypeScript or JSX.
- Do not use bare package imports. Use relative modules or `context.ui`.
- Do not use `eval` or inline scripts; the dashboard CSP blocks them.
- Prefix stylesheet selectors with the plugin id to avoid changing shell UI.
- Use `textContent` for values received from APIs or users.
- Release timers, event listeners, observers, and streams during cleanup.
- Plugins are trusted same-origin code and have the same backend access as the
  dashboard itself.

The server reports invalid plugins in the sidebar and from `GET /api/plugins`.
It rejects symlinks, traversal, unknown manifest fields, oversized files, and
stale asset fingerprints.
