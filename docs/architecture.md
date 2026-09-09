# Atelier architecture

[Back to the Atelier overview](../README.md)

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
