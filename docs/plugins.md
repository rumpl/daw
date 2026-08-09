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
  "backend": { "entry": "backend/index.js" },
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

`description`, `version`, `style`, and `backend` are optional. At least one page is
required. Pages with `sidebar: true` appear in the global Plugins navigation.
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

The entry exports `default` or `handler`, an async Fetch-style handler. Requests
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

Backend processes bind only to an ephemeral loopback port, start lazily on the
first request, and stop with the dashboard. They are trusted and run with the
same user permissions as the dashboard.

## Entry module

The entry is a browser-native ES module. It must export `mount(context)` and may
return a cleanup function.

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
`api.sessions`, `api.createChat`, `api.snapshot`, and `api.send` are also
available. Same-origin GET event streams can be opened with `EventSource`.

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
