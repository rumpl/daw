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
chats.

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
| `TAILSCALE_HOSTNAMES` | — | Hostnames to accept besides loopback |
| `ALLOWED_TAILSCALE_USERS` | — | Tailnet logins allowed through Tailscale Serve |
| `DAWUI_SESSION_DB` | docker-agent's default | Session database path |
| `DAWUI_WORKSPACE_HISTORY_FILE` | `<data>/dawui-workspaces.json` | Opened-project history path |
| `DAWUI_CHAT_PREFERENCES_FILE` | `<data>/dawui-chat-preferences.json` | Model and thinking preference path |
| `DAWUI_PLUGIN_DIR` | `<data>/dawui/plugins` | Global trusted frontend and Node backend plugin directory |
| `DAWUI_DEBUG` | — | Debug logging |

The server binds to `127.0.0.1` only; there is no host override. Workspaces are
limited to your home directory.

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
cmd/dawui                 server entrypoint: bind, port validation, signals
internal/protocol         wire types shared with the browser (+ TS generator)
internal/adapter          the typed docker-agent seam
internal/adapter/dagent   the real adapter: embeds the docker-agent SDK
internal/adapter/fake     deterministic fake agent used by the tests
internal/httpapi          routing, security, chat ownership, SSE, reducer
internal/pathsec          filesystem containment for working directories
internal/webassets        the built frontend, embedded with embed.FS
web/                      React + Vite + strict TypeScript
e2e/                      Playwright
```

Each live chat owns one `runtime.Runtime` and one `session.Session`, sharing a
single process-wide session store. A turn ends when the runtime's event channel
closes and both queues are drained. Runtime events are normalised into a small
discriminated union and streamed over SSE with monotonic event IDs, a replay
buffer and `Last-Event-ID` resume, so reconnecting never duplicates content.
Only one runtime ever drives a session inside this process; a second browser
attaches to the same live chat.

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
