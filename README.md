# docker-agent dashboard

A private web UI for [docker-agent](https://github.com/docker/docker-agent),
running on your own machine.

It embeds the docker-agent Go SDK in-process and drives one runtime per chat, so
your agents, models, credentials, toolsets, skills, permissions and sessions are
exactly the ones docker-agent already uses. Chats are stored in docker-agent's
own SQLite session database — start something in the browser, pick it up with
`docker agent run --session <id>` in a terminal, and back again.

Open a folder, hit **New chat**, and type.

---

## What you get

- **Streaming conversation** — assistant text, collapsible reasoning, tool
  activity, sub-agent transfers, token usage and cost, live over SSE.
- **Full turn control** — send, steer a running turn, queue a follow-up, stop.
- **Interactive prompts** — tool-confirmation and MCP elicitation dialogs, with
  approve once / always-allow-this-pattern / reject.
- **Model picker** — searchable, grouped by provider, showing each model's
  context window and price, from the models docker-agent itself reports.
- **Thinking budget** — switch effort level while the agent is idle.
- **Sessions** — list, resume and search every docker-agent session for the
  current directory; they survive restarts because they are docker-agent's.
- **Global plugins** — trusted runtime JavaScript modules can add sidebar items,
  pages, reuse dashboard components, call the complete API, and optionally run
  a proxied Node backend with an injected dashboard API library.
- **Works on your phone** — responsive down to 320px, over Tailscale.
- **One binary** — the frontend is embedded; `make start` serves API and UI from
  a single process bound to `127.0.0.1`.

---

## Requirements

- Go 1.26.5+ (or any Go with `GOTOOLCHAIN=auto`)
- Node 20+ and npm
- A working `docker-agent` install — if a chat reports no model, run
  `docker agent setup` or `docker agent doctor`

---

## Quick start

```bash
make deps      # frontend dependencies
make build     # frontend + Go binary
make start     # http://127.0.0.1:4788
```

Then open <http://127.0.0.1:4788>, put an absolute path in **Working
directory**, press **Open**, and press **New chat**.

For development with hot reload:

```bash
make dev       # Go API on :4788, Vite on :4789 proxying /api
make dev-fake  # same, with a deterministic fake agent (no model calls)
```

### Run agent execution in a Docker Sandbox (experimental)

Install Docker Sandboxes (`sbx`), then start the normal host dashboard with
both host and per-session sandbox execution available:

```bash
make start-sandbox                         # current directory
WORKSPACE=/absolute/project make start-sandbox
```

Open the usual <http://127.0.0.1:4788> URL. The browser, dashboard API, workspace
history, preferences, plugin catalog, and plugin backends remain on the host.
Before creating a session, the composer shows an **Execution target** select.
**Docker Sandbox** is the default; **Host** runs the same code-defined
`dashboard-coder` directly in the dashboard process. Changing the select saves
the choice in the backend and uses it as the default for subsequent new-chat
tabs, including after a dashboard restart. The select remains visible but
becomes read-only as soon as the session is created. Session rows show a box or
laptop icon for their execution target, and that target is retained on resume. Gossip-created child sessions inherit their parent's target.

For sandbox-targeted sessions, the model runtime, shell/filesystem tools, and
plugin MCP processes run in the sandbox. The selected workspace and global
plugin directory are mounted at their original absolute paths, so tool edits
are reflected on the host. Both targets persist into the same host-owned Docker
Agent session store; host-targeted sessions simply do not create a sandbox.

On the first launch for a new runner build, DAW creates one seed sandbox and
saves it with `sbx template save` as a content-addressed template. This one-time
bake currently takes about 40 seconds. The 96 MB runner then lives in the
sandbox image instead of being uploaded for every session. Measured session
sandbox startup from the baked template is about 4 seconds.

DAW does not prewarm ownerless sandboxes. **New chat** and the **+** button open
an unpersisted empty tab; the session is created only when its first message is
sent. Sending from a sandbox-targeted tab starts its dedicated sandbox directly
from the template. Closing the live chat stops its sandbox without removing it. Resuming the
session restarts the same sandbox and opens a new interactive `sbx exec`
connection. The host persists only the lifecycle session-to-sandbox mapping
under `~/.cagent/dawui/`; complete history stays in host SQLite.

The logical workspace is mounted into every session sandbox. A plugin execution
location may select another directory (for example a sibling Git worktree); DAW
mounts that path into only the selected session sandbox while keeping the
session indexed under the original workspace. Filesystem edits are still shared
through the host mounts.

The host and each runner communicate through a framed, bidirectional protocol
over the runner's `sbx exec` stdin/stdout pipes. No runner or callback port is
published, and this remains usable when the sandbox backend is remote. Runner
control, event streams, reverse session-store calls, and plugin callbacks are
multiplexed as independent HTTP connections. Plugin MCP processes call a
sandbox-local loopback proxy, which forwards only the authenticated backend
bridge over stdio; dashboard CSRF and internal backend tokens never enter the
sandbox. The browser-facing host API remains bound to loopback.

Docker Sandboxes keeps model credentials in its host-side secret store. Import
the providers you use before starting DAW. Provider HTTP clients honor the
sandbox's standard `HTTP_PROXY` and `HTTPS_PROXY` environment so the host proxy
injects credentials on the first real model request. DAW relaunches the runner
through `sbx exec` after sandbox initialization so it starts in the active proxy
process context; it sends no warm-up provider requests:

```bash
sbx secret import openai       # or anthropic, xai, etc.
make start-sandbox
```

The runner is constructed from `internal/dashboardagent.Build`; there is no
YAML copy of the agent. The generated Linux binary is local and gitignored. Its
content hash is part of the template tag, so rebuilding it produces a new
immutable template while existing sessions retain their original image. See
[`kits/daw-runner/README.md`](kits/daw-runner/README.md) for details.

### Desktop app (Electron)

```bash
make electron          # build the UI/backend and launch Electron
make package-electron  # write a DMG/ZIP or AppImage to electron/dist
```

Electron starts the Go backend through the per-session sandbox launcher. Docker
Sandbox is available and selected by default for new chats; the host target
remains available in the composer. The packaged app includes the Linux runner
kit and builds/reuses its content-addressed sandbox template on startup. It does
**not** reserve a TCP port: the backend listens on an owner-only Unix domain
socket, and Electron exposes that HTTP stream to the renderer through the
private `daw://localhost` protocol.
API calls, uploads, dynamic plugin modules and SSE all use that transport. The
packaged app contains the Go backend and embedded frontend, so no separately
installed server is needed. On macOS and Linux the desktop host imports your
login-shell environment before starting the backend, ensuring apps opened from
Finder or the desktop retain the same model credentials and tool `PATH` as the
CLI.

Desktop packaging currently targets macOS and Linux because it relies on Unix
domain sockets.

---

## Using it

**Start a chat.** Open a working directory in the sidebar. That directory is the
agent's `WorkingDir` — where its tools read and write. Then press **New chat**.

Directories you have opened are remembered in the browser and listed under
**Recent**, and the last one reopens automatically next time you load the page.
If it has since moved or is no longer inside your home directory, it is quietly
forgotten.

**Agent.** Every chat uses `dashboard-coder`, the coding agent assembled directly
with Docker Agent's Go SDK. Its system instruction includes the global plugin
contract, and its read-only `get_dashboard_developer_documentation` tool returns
the complete backend API and host-component reference before it writes a plugin.
The dashboard does not accept or resolve alternate agent configurations.

**Plugins.** Global plugins live in `~/.cagent/dawui/plugins` by default. Each
plugin is a browser-native ES module with a `plugin.json` manifest. Valid pages
appear in the sidebar and reload automatically when their files change. Plugins
receive the complete API client and the host's React instance, chat components,
Markdown renderer, tool cards, dialogs, and chat hooks. See
[`docs/plugins.md`](docs/plugins.md).

**Send and control a turn.**

| | |
| --- | --- |
| `Enter` | send |
| `Shift`+`Enter` | newline |
| `/` | command, skill and prompt-file autocomplete |
| **Steer** | inject a message into the running turn at the next safe point |
| **Follow-up** | queue a message to get its own turn afterwards |
| **Stop** | cancel the run and clear the queues |

Drafts are kept per chat, so switching away and back doesn't lose what you were
typing.

**Settings.** Model, thinking budget, Compact and Rename sit in the header on
desktop, and behind **Settings** on mobile. Model and thinking change only while
the agent is idle. Those choices are saved on the server: they are restored per
session after a restart, and the most recent choices become the defaults for new
chats. The Settings page can also save an LLM gateway URL to Docker Agent's
native user configuration. New chats use gateway mode; for Docker gateways,
Docker Agent obtains your signed-in Docker Desktop token automatically, so no
provider API keys are needed.

The model button opens a searchable list: the models your agent config names
come first, then any used earlier in the session, then the provider catalog
grouped by provider — each row showing its reference, context window and price
per million tokens. Type to filter, `↑`/`↓` to move, `Enter` to pick, `Esc` to
close.

**Sessions.** The sidebar starts with one live-session list spanning every open
project, so active work is reachable without switching directories first. Each
live row shows whether its turn is running or idle and has a **Close** action
that releases its server runtime without deleting the stored history. Every
session for the current directory is also listed below and searchable. Selecting
one resumes it with its real history from docker-agent's store.

---

## Tool execution

Every chat auto-approves tools. Explicit deny or ask patterns from your Docker
Agent configuration and `.agentsignore` still take precedence. When such a rule
raises a confirmation dialog, the permission pattern shown is exactly the
pattern granted by always-allow.

Permission patterns are a tool-call policy, not an OS boundary: they decide
whether a call runs, not what it can do once it does. The agent runs in this
process, on this host, with your user's permissions. For an isolated run, use
`docker agent run --sandbox` in a terminal, or run this dashboard inside a
container or VM.

---

## Configuration

All optional, all non-secret. Credentials are never read, stored or displayed by
this app — docker-agent resolves them itself from your environment and
credential helpers.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `4788` | TCP port, validated 1024–65535 |
| `DAWUI_SOCKET` | — | Listen on this Unix socket instead of TCP (used by Electron) |
| `TAILSCALE_HOSTNAMES` | — | Hostnames to accept besides loopback |
| `ALLOWED_TAILSCALE_USERS` | — | Tailnet logins allowed through Tailscale Serve |
| `DAWUI_SESSION_DB` | docker-agent's default | Session database path |
| `DAWUI_WORKSPACE_HISTORY_FILE` | `<data>/dawui-workspaces.json` | Opened-project history path |
| `DAWUI_CHAT_PREFERENCES_FILE` | `<data>/dawui-chat-preferences.json` | Model and thinking preference path |
| `DAWUI_PLUGIN_DIR` | `<data>/dawui/plugins` | Global trusted frontend and Node backend plugin directory |
| `DAWUI_DEBUG` | — | Debug logging |

The standalone server binds to `127.0.0.1` only; there is no host override.
When `DAWUI_SOCKET` is set, it opens only that owner-readable Unix socket and
ignores `PORT`. Workspaces are limited to your home directory.

---

## Reaching it from your phone

Serve it privately inside your tailnet with
[Tailscale Serve](https://tailscale.com/kb/1312/serve):

```bash
tailscale serve --bg http://127.0.0.1:4788
tailscale serve status
```

Then tell the dashboard which hostname to accept:

```bash
TAILSCALE_HOSTNAMES=your-machine.your-tailnet.ts.net make start
```

Optionally restrict it to yourself with
`ALLOWED_TAILSCALE_USERS=you@example.com`, and use tailnet ACLs to limit who can
reach the port. `--bg` keeps the proxy configuration across reboots; start the
app itself with `make start`.

---

## How it works

```
cmd/dawui                 server entrypoint: TCP/UDS bind, validation, signals
electron/                  desktop host: backend lifecycle and UDS protocol bridge
internal/protocol         wire types shared with the browser (+ TS generator)
internal/adapter          the typed docker-agent seam
internal/adapter/dagent   the real adapter: embeds the docker-agent SDK
internal/adapter/hybrid   execution-target router (host-backed catalog only)
internal/sessionstorebridge authenticated host store callback handler
internal/sessionstoreremote complete sandbox-side session.Store client
internal/stdiomux          bidirectional net.Conn mux over sbx exec stdin/stdout
internal/adapter/fake     deterministic fake agent used by the tests
internal/httpapi          routing, security, chat ownership, SSE, reducer
internal/pathsec          filesystem containment for working directories
internal/webassets        the built frontend, embedded with embed.FS
web/                      React + Vite + strict TypeScript
e2e/                      Playwright
```

Each live chat owns one `runtime.Runtime` and one `session.Session`. Host and
Docker Sandbox runtimes share one authoritative host-owned SQLite session
store; sandbox runners use authenticated reverse HTTP streams over stdio and
never create a local session database. A turn ends when the runtime's event channel
closes and both queues are drained. Runtime events are normalised into a small
discriminated union and streamed over SSE with monotonic event IDs, a replay
buffer and `Last-Event-ID` resume, so reconnecting never duplicates content.
Only one runtime ever drives a session; a second browser attaches to the same
live chat on whichever execution backend owns it.

The browser submits a working directory once, then uses its opaque ID for later
calls. Mutations carry a per-process CSRF token in a custom header, the
`Host` must be loopback or an explicitly configured Tailscale name, and
forwarded headers are trusted only from a loopback peer. Markdown is rendered
without raw HTML, links are restricted to `http`, `https` and `mailto`, and
remote images are off.

TypeScript protocol types are generated from the Go types by `make generate`; a
test fails the build if they drift.

---

## Where your data lives

Everything stays in docker-agent's own directories, resolved through its
`pkg/paths`:

| | |
| --- | --- |
| Config | `~/.config/cagent` |
| LLM gateway setting | `~/.config/cagent/config.yaml` (`models_gateway`) |
| Data | `~/.cagent` |
| Sessions | `~/.cagent/session.db` |
| Opened projects | `~/.cagent/dawui-workspaces.json` |
| Model and thinking choices | `~/.cagent/dawui-chat-preferences.json` |
| Global plugins | `~/.cagent/dawui/plugins/` |

The server keeps the ten most recently opened projects in an owner-only JSON
file. They appear under **Projects** in every browser connected to the server,
so a project opened on desktop is available when you visit from your phone.
Paths are revalidated against your home directory before they are advertised.
Model and thinking choices use the same owner-only, atomic-file persistence.

Sessions are created lazily on the first message, exactly like the CLI. In the
browser, `localStorage` holds only UI preferences, the current device's last
selection and unsent drafts.

Don't drive the *same* session from two places at once — this dashboard and
`docker agent run` in a terminal can share the store, but not one live session.

---

## Development

```bash
make generate    # regenerate web/src/protocol.gen.ts from the Go types
make typecheck   # go vet, staticcheck, tsc --noEmit
make test        # Go tests + Vitest
make test-e2e    # Playwright (Chromium)
make screenshots # render desktop + mobile shots to /tmp/uishots
```

Tests run against the fake adapter, so they never spend model tokens, pull an
image or start a sandbox. They cover path containment, session ownership, event
normalisation, SSE reconnect and replay, tool-confirmation and elicitation round
trips, permission-pattern fidelity, CSRF and origin checks, and Markdown safety.

`scripts/smoke-real.sh` exercises a real model end to end. It asks for
confirmation first and spends tokens.

---

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `port 4788 already in use` | `lsof -nP -iTCP:4788 -sTCP:LISTEN`, or set `PORT` |
| `that path is outside your home directory` | Move or clone the project under your home directory |
| No models listed | Run `docker agent setup` or `docker agent doctor` |
| `the agent is busy` | Model, thinking and mode changes need an idle agent; use Steer or Follow-up |
| Blank page | The frontend isn't built into the binary: `make build` |
| Reconnecting badge | The server restarted; the client resnapshots automatically |
