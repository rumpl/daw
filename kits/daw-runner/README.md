# DAW sandbox runner kit

This schema-v2 kit extends Docker Sandboxes' built-in `docker-agent` kit and
runs a small authenticated DAW execution service on sandbox port 8080. It does
not serve the dashboard UI or host DAW's plugin-management control plane.

The runner constructs `dashboard-coder` through
`internal/dashboardagent.Build`; there is deliberately no YAML copy of the
agent definition.

The Linux runner binary is generated locally and bundled under
`files/home/.local/lib/daw-runner`:

```sh
make build-runner-kit
```

The normal entry point is:

```sh
WORKSPACE=/absolute/project make start-sandbox
```

The launcher:

1. builds the host dashboard and cross-compiles the Linux runner;
2. stages a temporary kit with a persistent per-sandbox authentication token;
3. mounts the selected workspace and host DAW plugin directory;
4. starts the sandbox runner detached through `go-sbx`, or hot-updates the
   runner binary when reusing an existing sandbox;
5. discovers its ephemeral loopback port through `sbx ports --json`;
6. waits for authenticated `/v1/health` and primes model credential proxying;
   and
7. starts the host dashboard with the remote adapter configured.

Hot-updating the runner preserves its session database. Changes to the kit
itself (credentials, resources, or setup commands) still require removing and
recreating the sandbox.

## Runtime boundary

On the host:

- browser assets and `/api` control plane;
- workspace history and chat preferences;
- plugin discovery, lifecycle, frontend assets, and Node backends.

In the sandbox:

- code-defined Docker Agent runtime;
- model calls and shell/filesystem tools;
- local plugin MCP child processes;
- the runner's Docker Agent session database.

MCP declarations are discovered on the host and sent with each open-chat
request. Local MCP commands execute from the mounted plugin directory. Their
`DAW_API_ORIGIN` is rewritten to a random-port `host.docker.internal` bridge.
The sandbox receives a bridge-only token, while the bridge permits only the
calling plugin's backend-proxy route and adds the real host credentials after
validation.

The runner port is loopback-published and requires a random bearer token. The
token is stored owner-only under `~/.cagent/dawui/sandbox-tokens` and staged
owner-only into the kit.

The model-provider credential declarations are repeated explicitly in this kit.
Current Docker Sandboxes releases do not carry those declarations through
`extends: docker-agent`; without the explicit declarations, the placeholder API
key reaches the provider unchanged and produces a `proxy-managed` 401. Actual
keys remain in the host-side `sbx secret` store. After setup and readiness, the
launcher also sends harmless provider-catalog requests through the explicit
proxy. This primes credential injection before the SDK's first model request;
doing it in the kit startup hook is too early in the sandbox lifecycle.

The generated binary is gitignored. Distributing this kit will require
multi-architecture images or release artifacts rather than local staging.
