# DAW sandbox runner kit and template

The schema-v2 kit extends Docker Sandboxes' built-in `docker-agent` kit and
configures a small authenticated DAW execution service on sandbox port 8080. It
does not serve the dashboard UI or host DAW's plugin-management control plane.

The runner constructs `dashboard-coder` through
`internal/dashboardagent.Build`; there is deliberately no YAML copy of the
agent definition.

## Kit versus template

Docker Sandboxes treats these as separate primitives:

- the **kit** declares credentials, ports, startup hooks, instructions, and
  files to apply to a sandbox;
- a **template** is a saved sandbox container image used as the root filesystem
  for later sandboxes.

Applying the 96 MB runner as a kit file to every sandbox took about 30 seconds.
DAW therefore uses the full kit only once to create a clean seed sandbox, then
runs:

```sh
sbx template save <seed> daw-runner:<content-hash>
```

Normal session sandboxes use that template plus a lightweight staged copy of
the kit. The lightweight kit omits the already-baked runner and contains only
small configuration such as the unique per-sandbox bearer token. Nothing is
mounted from the host to provide the executable.

The template tag hashes both `spec.yaml` and the Linux runner binary. Rebuilding
either produces a new immutable template. Existing session sandboxes continue
to use the image they were created with.

The Linux runner binary is generated locally under
`files/home/.local/lib/daw-runner`:

```sh
make build-runner-kit
```

The normal entry point is:

```sh
WORKSPACE=/absolute/project make start-sandbox
```

The launcher calls `sandboxrunner.EnsureTemplate`. The first launch for a new
content hash performs the expensive seed bake once; later launches reuse the
saved template. On the current development machine the measured times were:

- one-time template bake: about 43 seconds;
- new session sandbox from the template, including runner health: about 4.2
  seconds.

You can inspect or remove local templates with:

```sh
sbx template ls
sbx template rm daw-runner:<content-hash>
```

The host session-sandbox adapter then:

1. creates a dedicated sandbox only when a user or gossip child session opens;
2. mounts the logical workspace, plugin directory, and any plugin-selected
   execution directory at their original absolute paths;
3. discovers each runner's ephemeral loopback port and authenticates with a
   per-sandbox token;
4. stops a sandbox when no live chat owns its session; and
5. restarts that same sandbox and rediscovers its port on resume.

Bootstrap and global settings do not start a sandbox. Before the first chat,
DAW reports the standard thinking levels but leaves model and tool catalogs
empty; the opened chat resolves its real model and exposes its model catalog.

The host stores the session-to-sandbox index in
`~/.cagent/dawui/sandbox-sessions-<workspace-hash>.json`.

A pre-existing workspace-level `daw-<workspace>-<hash>` sandbox is imported as
a legacy mapping so its existing sessions remain visible and resumable. New
sessions always receive dedicated `daw-session-<hash>` sandboxes.

## Runtime boundary

On the host:

- browser assets and `/api` control plane;
- workspace history, chat preferences, and session-to-sandbox index;
- plugin discovery, lifecycle, frontend assets, and Node backends.

In each session sandbox:

- exactly one newly-created Docker Agent session;
- code-defined Docker Agent runtime;
- model calls and shell/filesystem tools;
- local plugin MCP child processes;
- the session's Docker Agent database.

MCP declarations are discovered on the host and sent with each open-chat
request. Local MCP commands execute from the mounted plugin directory. Their
`DAW_API_ORIGIN` is rewritten to a random-port `host.docker.internal` bridge.
The sandbox receives a bridge-only token, while the bridge permits only the
calling plugin's backend-proxy route and adds the real host credentials after
validation.

The runner port is loopback-published and requires a random bearer token. The
token is stored owner-only under `~/.cagent/dawui/sandbox-tokens` and applied by
the lightweight per-sandbox kit; it is not baked into the reusable template.

Actual model keys remain in the host-side `sbx secret` store. The sandbox sees
only `proxy-managed`, and the host proxy replaces provider authorization headers
according to the kit declarations. After `sbx run` completes, DAW relaunches the
startup-hook runner once through `sbx exec`, placing it in the fully initialized
credential-proxy process context before it accepts model requests. The Docker
Agent HTTP transport gives the sandbox-provided `HTTP_PROXY` and `HTTPS_PROXY`
environment precedence over Docker Desktop's host-service socket. Its OpenAI
WebSocket dialer also honors `HTTP_PROXY`. Consequently, the first real provider
request follows the documented credential-injection path without a warm-up
request.
