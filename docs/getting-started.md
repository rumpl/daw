# Getting started with Atelier

[Back to the Atelier overview](../README.md)

## Requirements

- Go 1.26.5+ (or any Go with `GOTOOLCHAIN=auto`)
- Node 20+ and npm
- Docker Sandboxes (`sbx`), authenticated to the configured kit repository
- A working `docker-agent` install — if a chat reports no model, run
  `docker agent setup` or `docker agent doctor`

---

## Quick start

Install Docker Sandboxes (`sbx`) and authenticate to the sandbox-kit repository,
then run:

```bash
make dev
```

This is the single supported way to launch Atelier from the repository. It
builds and publishes the current sandbox template and kit, starts the
sandbox-enabled Go API
on `127.0.0.1:4788`, starts Vite with hot reload on `127.0.0.1:4789`, and opens
the UI in your browser. To use another project as the default sandbox workspace:

```bash
WORKSPACE=/absolute/project make dev
```

The development ports are fixed: the API uses `4788` and Vite uses `4789`.
`make dev-fake` remains available for deterministic UI development and tests;
it intentionally does not enable sandboxes or make model calls.

Docker Sandbox is available and selected by default for new chats; **Host** runs
the same code-defined `dashboard-coder` directly in the backend process. The
browser, dashboard API, workspace history, preferences, plugin catalog, and
plugin backends remain on the host.

For sandbox-targeted sessions, the model runtime and shell/filesystem tools run
in the sandbox. Plugin MCP configuration remains global and identical across
execution targets: local command MCP processes always run on the host and a
byte-transparent stdio relay injects their transports into sandbox runtimes;
remote URL MCP servers are contacted normally by the owning runtime. The
selected workspace and global
plugin directory are mounted at their original absolute paths, so tool edits
are reflected on the host. Both targets persist into the same host-owned Docker
Agent session store; host-targeted sessions simply do not create a sandbox.

Atelier's build bakes the Linux runner into an immutable template derived from
Docker Agent's supported image, then publishes a small content-addressed sandbox
kit that selects it. The launcher uses that immutable kit reference directly, so
application startup does not create a seed sandbox or copy the roughly 100 MB
runner through the kit materializer for every session. Docker Sandboxes pulls the
template on the first session that uses it and caches its image layers for later
sessions.

Atelier does not prewarm ownerless sandboxes. **New chat** and the **+** button open
an unpersisted empty tab; the session is created only when its first message is
sent. Sending from a sandbox-targeted tab starts its dedicated sandbox from the
published kit. Closing the live chat stops its sandbox without removing it. Resuming the
session restarts the same sandbox and opens a new interactive `sbx exec`
connection. The host persists only the lifecycle session-to-sandbox mapping
under `~/.cagent/dawui/`; complete history stays in host SQLite.

The logical workspace is mounted into every session sandbox. Plugin MCP
processes remain host-side and therefore do not require plugin directories to be
mounted into the sandbox. A plugin execution
location may select another directory (for example a sibling Git worktree); Atelier
mounts that path into only the selected session sandbox while keeping the
session indexed under the original workspace. Filesystem edits are still shared
through the host mounts.

The host and each runner communicate through a framed, bidirectional protocol
over the runner's `sbx exec` stdin/stdout pipes. No runner or callback port is
published, and this remains usable when the sandbox backend is remote. Runner
control, event streams, reverse session-store calls, and plugin callbacks are
multiplexed as independent HTTP connections. Host-side plugin MCP processes use the same authenticated backend
API transport as host agents. For sandbox agents, their MCP stdio is relayed
byte-for-byte over an independent reverse stream, so the sandbox never receives
the command, host environment, or plugin credentials. The browser-facing host
API remains bound to loopback.

Docker Sandboxes keeps model credentials in its host-side secret store. Import
the providers you use before starting Atelier. Provider HTTP clients honor the
sandbox's standard `HTTP_PROXY` and `HTTPS_PROXY` environment so the host proxy
injects credentials on the first real model request. Atelier relaunches the runner
through `sbx exec` after sandbox initialization so it starts in the active proxy
process context; it sends no warm-up provider requests:

```bash
sbx secret import openai       # or anthropic, xai, etc.
make dev
```

The runner is constructed from `internal/dashboardagent.Build`; there is no
YAML copy of the agent. The generated Linux binary is local and gitignored. `make publish-sandbox-kit`
validates the complete kit, tags it from its content, pushes it to Docker Hub,
and writes the immutable OCI reference to `bin/daw-runner-kit.ref`. See
[`../kits/daw-runner/README.md`](../kits/daw-runner/README.md) for details.

### Desktop app (Electron)

```bash
make electron          # build the UI/backend and launch Electron
make package-electron  # write a DMG/ZIP or AppImage to electron/dist
```

Electron starts the Go backend through the per-session sandbox launcher. Docker
Sandbox is available and selected by default for new chats; the host target
remains available in the composer. The packaged app includes the Linux runner
kit reference and pulls that kit when the first sandbox session is created. It does
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
