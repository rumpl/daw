# Atelier user guide

[Back to the Atelier overview](../README.md)

## Using it

**Start a chat.** Open a working directory in the sidebar. That directory is the
agent's `WorkingDir` — where its tools read and write. Then press **New chat**.

Directories you have opened are remembered in the browser and listed under
**Recent**, and the last one reopens automatically next time you load the page.
If it has since moved or is no longer inside your home directory, it is quietly
forgotten.

**Agent.** Every chat uses `dashboard-coder`, the coding agent assembled directly
with the Docker Agent Go SDK. Its system instruction directs plugin work through
the built-in `atelier-dashboard-plugin-development` skill, which provides the
complete backend API and host-component reference alongside locally discovered
skills. The dashboard does not accept or resolve alternate agent configurations.

**Plugins.** Global plugins live in `~/.cagent/dawui/plugins` by default. Each
plugin is a browser-native ES module with a `plugin.json` manifest. Valid pages
appear in the sidebar and reload automatically when their files change. Plugins
receive the complete API client and the host's React instance, chat components,
Markdown renderer, tool cards, dialogs, and chat hooks. See
[`plugins.md`](plugins.md).

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

**Quitting during a sandbox run.** The desktop window and the agent worker have
separate lifetimes. You can quit Atelier completely while a sandboxed agent is
working; the background worker and sandbox continue the run. Opening Atelier
again reconnects to that worker, restores the session, and shows the events that
arrived while the app was closed.

---

## Tool execution

Every chat auto-approves tools; there is no confirmation dialog. Explicit deny
patterns from your Docker Agent configuration and `.agentsignore` still take
precedence, while `ask` patterns are approved automatically. MCP elicitation
requests are cancelled automatically because the dashboard has no elicitation UI.

Permission patterns are a tool-call policy, not an OS boundary: they decide
whether a call runs, not what it can do once it does. The agent runs in this
process, on this host, with your user's permissions. For an isolated run, use
`docker agent run --sandbox` in a terminal, or run Atelier inside a
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

Then tell Atelier which hostname to accept:

```bash
TAILSCALE_HOSTNAMES=your-machine.your-tailnet.ts.net make dev
```

Optionally restrict it to yourself with
`ALLOWED_TAILSCALE_USERS=you@example.com`, and use tailnet ACLs to limit who can
reach the port. `--bg` keeps the proxy configuration across reboots; keep the
app itself running with `make dev`.

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

Don't drive the *same* session from two places at once — Atelier and
`docker agent run` in a terminal can share the store, but not one live session.

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
