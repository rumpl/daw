# DAW sandbox runner kit and template

The schema-v2 kit extends Docker Sandboxes' built-in `docker-agent` kit and
configures a small authenticated DAW execution service reached through an
interactive `sbx exec` stdin/stdout stream. It publishes no sandbox port and
does not serve the dashboard UI or host DAW's plugin-management control plane.

The runner constructs `dashboard-coder` through
`internal/dashboardagent.Build`; there is deliberately no YAML copy of the
agent definition.

## Kit versus template

Docker Sandboxes treats these as separate primitives:

- the **kit** declares credentials, ports, the no-op agent entrypoint,
  instructions, and files to apply to a sandbox;
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
- new session sandbox from the template, including runner health: about 4.6
  seconds with `sbx` v0.38.0. Roughly 3.7 seconds of that is the underlying
  `sbx run --template` operation.

You can inspect or remove local templates with:

```sh
sbx template ls
sbx template rm daw-runner:<content-hash>
```

The host dashboard exposes **Docker Sandbox** and **Host** in an unpersisted
empty composer tab. The backend persists the latest selection as the default
for later tabs and dashboard restarts. The session is created on its first send;
its target is immutable after creation, and agent-made child sessions inherit
their parent's target. For a sandbox-targeted session,
the session-sandbox adapter:

1. creates a dedicated sandbox only when the session opens;
2. mounts the logical workspace, plugin directory, and any plugin-selected
   execution directory at their original absolute paths;
3. starts a long-running `sbx exec`, authenticates with a per-sandbox token,
   and multiplexes control and reverse RPC over its stdin/stdout pipes;
4. stops a sandbox when no live chat owns its session; and
5. restarts that same sandbox and opens a fresh stdio connection on resume.

Bootstrap and global settings do not start a sandbox. Their model and tool
catalogs are resolved by the host Docker Agent adapter and the selected
preferences are applied when either kind of session opens.

The host stores only lifecycle mappings (session ID, sandbox name, and mounted
working directory) in
`~/.cagent/dawui/sandbox-sessions-<workspace-hash>.json`. It does not duplicate
titles, message counts, attributes, lineage, or other catalog data. Legacy
sandbox-local catalogs are intentionally not imported. New sandbox-targeted
sessions receive dedicated `daw-session-<hash>` sandboxes.

## Runtime boundary

On the host:

- browser assets and `/api` control plane;
- workspace history, chat preferences, and the lifecycle-only session-to-sandbox index;
- plugin discovery, lifecycle, frontend assets, and Node backends;
- Docker Agent runtime and the authoritative session store for every execution target;
- authenticated session-store and plugin callback handlers served over each
  runner's reverse stdio streams.

In each session sandbox:

- exactly one live Docker Agent session runtime;
- code-defined Docker Agent runtime;
- model calls and shell/filesystem tools;
- local plugin MCP child processes;
- a complete remote `session.Store` client; there is no sandbox-local session database.

The host passes `DAW_SESSION_STORE_TOKEN` only to the runner process. The
runner performs a versioned store handshake before serving its side of the
stdio mux. Store mutations are serialized, carry idempotency IDs, and are
written synchronously to the host database.

MCP declarations are discovered on the host and sent with each open-chat
request. Local MCP commands execute from the mounted plugin directory. Their
`DAW_API_ORIGIN` points to a fixed sandbox-local loopback proxy. That proxy
opens reverse streams over stdio; the host bridge still permits only the
calling plugin's backend route and adds real host credentials after validation.

The runner API also travels over stdio and retains its random bearer token as
defense in depth. The token is stored owner-only under
`~/.cagent/dawui/sandbox-tokens` and applied by the lightweight per-sandbox kit;
it is not baked into the reusable template.

Actual model keys remain in the host-side `sbx secret` store. The sandbox sees
only `proxy-managed`, and the host proxy replaces provider authorization headers
according to the kit declarations. When the configured models gateway is a
Docker HTTPS endpoint, DAW composes Docker Agent's generated `sbx-login` mixin
into each new session sandbox. That mixin exposes only a proxy sentinel and has
the host proxy inject a fresh Docker login token for the exact gateway hostname;
the token never enters the VM. DAW also allowlists the configured gateway in the
sandbox network policy. Because credential policy is fixed at creation, a
stopped session sandbox is recreated on resume if its Docker gateway hostname
changed; host-owned session history is retained. The kit intentionally has no
`setup.startup` runner hook: after `sbx run` completes, DAW starts the runner
exactly once through `sbx exec`, placing it in the fully initialized
credential-proxy process context before it accepts model requests. The Docker
Agent HTTP transport gives the sandbox-provided `HTTP_PROXY` and `HTTPS_PROXY`
environment precedence over Docker Desktop's host-service socket. Its OpenAI
WebSocket dialer also honors `HTTP_PROXY`. Consequently, the first real provider
request follows the documented credential-injection path without a warm-up
request.
