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
  approve once / always-allow-this-pattern / approve-for-session / reject.
- **Model picker** — searchable, grouped by provider, showing each model's
  context window and price, from the models docker-agent itself reports.
- **Thinking budget** — switch effort level while the agent is idle.
- **Tool approval modes** — docker-agent's own `strict` / `balanced` /
  `autonomous` safety modes.
- **Sessions** — list, resume and search every docker-agent session for the
  current directory; they survive restarts because they are docker-agent's.
- **Works on your phone** — responsive down to 320px, over Tailscale.
- **One binary** — the frontend is embedded; `make start` serves API and UI from
  a single process bound to `127.0.0.1`.

---

## Requirements

- Go 1.26.5+ (or any Go with `GOTOOLCHAIN=auto`)
- Node 20+ and npm
- A working `docker-agent` install — if a chat reports no model, run
  `docker agent setup` or `docker agent doctor`

Pinned to `github.com/docker/docker-agent v1.122.0`.

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
If it has since moved or is no longer inside your allowed roots, it is quietly
forgotten.

**Agents.** Chats use `coder`, docker-agent's built-in coding agent, with its
`librarian` and `planner` sub-agents. Point `DEFAULT_AGENT` at another built-in
name, an agent YAML path, or an OCI reference to change that.

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

**Settings.** Model, thinking budget, tool approval mode, Compact and Rename sit
in the header on desktop, and behind **Settings** on mobile. Model, thinking and
approval mode change only while the agent is idle. Model and thinking choices
are saved on the server: they are restored per session after a restart, and the
most recent choices become the defaults for new chats.

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

## Tool approval modes

These are docker-agent's own session safety modes, applied through
`session.SetSafetyPolicy` and evaluated by its permission engine. Your
configured allow / ask / deny patterns and `.agentsignore` are evaluated
independently and always win over the mode.

| Mode | Behaviour |
| --- | --- |
| `autonomous` | auto-approves every tool call *(default — see `DEFAULT_SAFETY`)* |
| `balanced` | auto-approves calls classified safe, asks on destructive and unknown ones |
| `strict` | asks before every tool call, including read-only ones |

New chats start in the configured default; resumed sessions keep the mode stored
with the session.

When a confirmation dialog appears, the permission pattern it shows you is
exactly the pattern granted if you choose always-allow — it comes from
docker-agent's own pattern builder, so the dialog and the grant can't disagree.

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
| `WORKSPACE_ROOTS` | your home directory | Path-list of directories that may be opened |
| `DEFAULT_AGENT` | `coder` | Agent used when none is chosen |
| `DEFAULT_SAFETY` | `autonomous` | `strict`, `balanced` or `autonomous` for new chats |
| `TAILSCALE_HOSTNAMES` | — | Hostnames to accept besides loopback |
| `ALLOWED_TAILSCALE_USERS` | — | Tailnet logins allowed through Tailscale Serve |
| `DAWUI_SESSION_DB` | docker-agent's default | Session database path |
| `DAWUI_WORKSPACE_HISTORY_FILE` | `<data>/dawui-workspaces.json` | Opened-project history path |
| `DAWUI_CHAT_PREFERENCES_FILE` | `<data>/dawui-chat-preferences.json` | Model and thinking preference path |
| `DAWUI_DEBUG` | — | Debug logging |

The server binds to `127.0.0.1` only; there is no host override.

To open directories outside your home directory:

```bash
WORKSPACE_ROOTS="$HOME:/Volumes/code:/opt/projects" make start
```

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
internal/pathsec          filesystem containment for directories and configs
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

The browser gets opaque IDs for directories and agent sources and never sends
paths back. Mutations carry a per-process CSRF token in a custom header, the
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

The server keeps the ten most recently opened projects in an owner-only JSON
file. They appear under **Projects** in every browser connected to the server,
so a project opened on desktop is available when you visit from your phone.
Paths are revalidated against `WORKSPACE_ROOTS` before they are advertised.
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
| `outside the allowed workspace roots` | Add it to `WORKSPACE_ROOTS` and restart |
| No models listed | Run `docker agent setup` or `docker agent doctor` |
| `the agent is busy` | Model, thinking and mode changes need an idle agent; use Steer or Follow-up |
| Blank page | The frontend isn't built into the binary: `make build` |
| Reconnecting badge | The server restarted; the client resnapshots automatically |
| A remote agent won't load | Pulling an OCI agent needs explicit confirmation in the UI |
