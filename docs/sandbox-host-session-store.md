# Handoff: make the host authoritative for sandbox session storage

## Status

Implemented, with one transport amendment: the host and runner do not expose
network listeners to each other. They multiplex ordinary HTTP connections over
a long-running `sbx exec` stdin/stdout stream (`internal/stdiomux`). The runner
API flows host-to-sandbox, while session-store and plugin callback requests open
reverse streams sandbox-to-host. Consequently `host.docker.internal`, published
runner ports, and `DAW_SESSION_STORE_URL` are not required. The typed store
routes, authentication, body limits, error mapping, ordering, idempotency, and
host ownership described below remain the protocol semantics.

The current sandbox implementation persists Docker Agent sessions inside each
session sandbox. The host then maintains a second summary index and uses
`internal/adapter/hybrid` to merge the host Docker Agent store with that index.
This is the wrong long-term ownership model.

The desired model is:

```text
                              authoritative session.Store
                                         │
                                         ▼
Browser/API ── Host DAW ───────────────── host SQLite database
                │                                ▲
                │                                │ store RPC
                └── sandbox lifecycle ── sandbox runner
                                              │
                                              └── Docker Agent runtime
                                                  using remote session.Store
```

A sandbox runner must implement Docker Agent's `session.Store` interface by
sending store operations to a host-owned store service. The host is the only
durable owner of session history for both execution targets. Stopping or
removing a sandbox must never make its history unavailable.

## Goals

1. Use one host-owned Docker Agent session store for Host and Docker Sandbox
   sessions.
2. Give the sandbox runtime a real implementation of
   `github.com/docker/docker-agent/pkg/session.Store` that forwards operations
   to the host.
3. Make session listing and stored-session reads entirely host-local. They must
   not start, resume, inspect, or contact a sandbox.
4. Persist `daw.execution.target` in authoritative session attributes and use it
   to route resumes.
5. Retain one sandbox per live/persistent sandbox-targeted session and the
   existing stop/resume/remove lifecycle semantics.
6. Preserve complete Docker Agent session fidelity: messages, message updates,
   summaries, errors, sub-sessions, usage/cost, titles, stars, attributes,
   attachments/parts, and lineage.
7. Keep the browser/API/plugin control plane on the host and loopback-only.
8. Keep the store callback endpoint separate from the browser-facing DAW API.

## Non-goals

- Do not change where model calls or tools execute.
- Do not let a session change execution target after creation.
- Do not mount the host SQLite file into a sandbox.
- Do not let multiple SQLite connections in different VMs access the same DB.
- Do not persist only DAW's normalized `protocol.Item` representation. The
  remote implementation must preserve the native Docker Agent session model.
- Do not create or prewarm ownerless sandboxes.
- Do not redesign the frontend as part of this work.
- Do not migrate or preserve sessions stored by the old sandbox-local store.
  Existing sandbox-only history may be discarded when this architecture lands.

## Current architecture and the problem

Relevant code:

- `internal/adapter/dagent/adapter.go`
  - opens a native SQLite `session.Store` in `dagent.New`;
  - passes it to each runtime with `daruntime.WithSessionStore(a.store)`;
  - implements list/read by querying that store.
- `cmd/daw-runner/main.go`
  - currently constructs another `dagent.Adapter` with a sandbox-local
    `session.db`.
- `internal/adapter/sandbox/adapter.go`
  - keeps `record.Summary` and attributes in
    `~/.cagent/dawui/sandbox-sessions-<workspace-hash>.json`;
  - starts a sandbox to perform detailed session reads.
- `internal/adapter/hybrid/adapter.go`
  - lists both adapters and merges their summaries;
  - probes both catalogs to discover which runtime owns a session.
- `internal/runnerapi/server.go` and `internal/adapter/remote/adapter.go`
  - expose and consume sandbox-local list/read routes.

Consequences of the current model:

- there are multiple authoritative databases;
- the host has a duplicate, lossy summary index for sandbox sessions;
- list/read/routing logic depends on knowing which store to query;
- detailed history may require booting a stopped sandbox;
- sandbox deletion is coupled to history availability;
- deletion, target routing, and lineage are unnecessarily complex.

## Docker Agent store contract

The interface currently lives at:

`/Users/rumpl/dev/cagent/pkg/session/store.go`

At the time this document was written it contains:

```go
type Store interface {
    AddSession(context.Context, *session.Session) error
    GetSession(context.Context, string) (*session.Session, error)
    GetSessions(context.Context) ([]*session.Session, error)
    GetSessionSummaries(context.Context) ([]session.Summary, error)
    DeleteSession(context.Context, string) error
    UpdateSession(context.Context, *session.Session) error
    SetSessionStarred(context.Context, string, bool) error

    AddMessage(context.Context, string, *session.Message) (int64, error)
    UpdateMessage(context.Context, int64, *session.Message) error
    AddSubSession(context.Context, string, *session.Session) error
    AddSummary(context.Context, string, session.Item) error
    AddError(context.Context, string, *session.Error) error

    UpdateSessionTokens(context.Context, string, int64, int64, float64) error
    UpdateSessionTitle(context.Context, string, string) error
    Close() error
}
```

Re-read the interface before implementation; the cagent checkout is under
`/Users/rumpl/dev/cagent` and may have changed. Add a compile-time assertion:

```go
var _ session.Store = (*RemoteStore)(nil)
```

Docker Agent already has a `RemoteSessionStore` in
`pkg/runtime/remote_runtime.go`, but inspect it carefully before reusing it. At
present several methods appear stubbed or unsupported; it is not automatically
suitable for a durable DAW store bridge.

## Proposed components

Names are suggestions, not requirements.

### 1. Host `SessionStoreBridge`

Add a host-only package such as:

```text
internal/sessionstorebridge/server.go
internal/sessionstorebridge/wire.go
```

It should:

- own a reference to the same host `session.Store` used by host-targeted
  runtimes;
- expose HTTP methods for every `session.Store` operation;
- listen on a random host port reachable as `host.docker.internal:<port>`;
- not be mounted under the browser-facing `/api` server;
- impose request-body limits and strict JSON decoding;
- translate `session.ErrSessionNotFound` and equivalent failures into stable
  wire error codes;
- never close the host store in response to a runner's `Close` call.

The existing MCP callback bridge is a useful host-networking reference, but
session storage should have its own routes.

### 2. Sandbox `RemoteStore`

Add a package usable by `cmd/daw-runner`, for example:

```text
internal/sessionstoreremote/store.go
internal/sessionstoreremote/client.go
```

It should implement every method of `session.Store` against the host bridge.
Use one shared HTTP client with keep-alive connections, bounded response bodies,
context propagation, and explicit request timeouts.

`Close` should only close idle HTTP connections and mark the client closed. It
must not close the host SQLite store.

Start with synchronous operations. `UpdateMessage` may be called for every
streaming delta, but correctness is more important than premature batching. If
write coalescing is added later, it must provide a flush/barrier before a run is
reported complete, before compaction, before resume reads, and during clean
shutdown. The final message update must never be lost.

### 3. Store injection in `dagent.Adapter`

Refactor `internal/adapter/dagent.New` so the caller can supply a
`session.Store`, rather than always opening SQLite. A possible configuration:

```go
type Config struct {
    Logger       *slog.Logger
    SessionDB    string
    SessionStore session.Store
    OwnStore     bool
    StoreLabel   string
}
```

Exact fields may differ, but ownership must be explicit:

- Host DAW opens and owns SQLite.
- Host `dagent.Adapter` uses that store.
- The store bridge uses that same store.
- Sandbox `dagent.Adapter` receives `RemoteStore` and must not open a local DB.
- Closing a sandbox adapter closes only its remote client, not the host DB.

Keep `daruntime.WithSessionStore(a.store)` as the runtime integration point.

### 4. Runner configuration

The runner needs the host bridge address:

```text
DAW_SESSION_STORE_URL
```

Pass it to the runner process launched through `sbx exec`. The host store bridge
must be listening before the runner starts.

`cmd/daw-runner/main.go` should fail closed if remote-store configuration is
missing in sandbox mode. It must not silently fall back to sandbox-local SQLite,
because that would recreate split-brain storage.

## Suggested wire API

Use typed routes or an equivalent versioned RPC protocol. A straightforward
shape is:

```text
POST   /v1/store/sessions                         AddSession
GET    /v1/store/sessions/{id}                    GetSession
GET    /v1/store/sessions                         GetSessions
GET    /v1/store/session-summaries                GetSessionSummaries
PUT    /v1/store/sessions/{id}                    UpdateSession
DELETE /v1/store/sessions/{id}                    DeleteSession
PUT    /v1/store/sessions/{id}/starred            SetSessionStarred
POST   /v1/store/sessions/{id}/messages           AddMessage
PUT    /v1/store/messages/{messageId}             UpdateMessage
POST   /v1/store/sessions/{id}/sub-sessions       AddSubSession
POST   /v1/store/sessions/{id}/summaries          AddSummary
POST   /v1/store/sessions/{id}/errors             AddError
PUT    /v1/store/sessions/{id}/usage              UpdateSessionTokens
PUT    /v1/store/sessions/{id}/title              UpdateSessionTitle
```

Returning native `session.Session`, `session.Message`, `session.Item`, and
`session.Summary` as JSON is acceptable only after round-trip tests prove that
all required fields survive. The native types intentionally omit mutexes and
runtime-only fields from JSON, but attachments/document parts, tool calls,
usage, attributes, errors, and sub-sessions require explicit coverage.

`AddMessage` must return the host-generated `int64` message ID. Subsequent
`UpdateMessage` calls must use that ID.

### Ordering and retries

Mutation ordering matters. At minimum, serialize mutations from one
`RemoteStore` client so that `AddSession` precedes message writes and
`AddMessage` precedes its updates. Per-session serialization is acceptable if
sub-session ordering is also tested.

Do not blindly retry non-idempotent writes after ambiguous network failures.
Prefer an operation ID on every mutation and host-side idempotency handling. If
idempotency is deferred, propagate ambiguous failures rather than risking
message or summary duplication, and document the limitation.

## Store metadata

The bridge must enforce the execution-target attribute on writes from sandbox
runners rather than depending on each caller to set it correctly:

```text
daw.execution.target = sandbox
```

It should also preserve parent/root lineage and any sandbox ownership attribute
needed for lifecycle cleanup. Host-targeted sessions should likewise receive
`daw.execution.target=host` before their initial `AddSession` reaches the same
store.

Re-read runtime usage of `GetSessions` and `GetSessionSummaries` before deciding
whether the remote implementation needs complete catalog results or can use a
narrower result without changing Docker Agent behavior.

## New create/resume flow

### New sandbox session

1. Browser sends the first message from an unpersisted tab.
2. Host creates the chat ID and chooses the immutable sandbox target.
3. Host allocates sandbox lifecycle metadata.
4. Host starts the per-session sandbox and runner.
5. Runner constructs `dagent.Adapter` with `RemoteStore`.
6. Docker Agent creates the native session; `AddSession` writes synchronously to
   host SQLite and host enforces `daw.execution.target=sandbox`.
7. Runner returns the session ID to the host.
8. Host stores only lifecycle data needed to find/manage the sandbox—not a
   duplicate session summary.
9. Sidebar/list/read immediately use the host catalog.

If step 6 fails, opening the chat must fail. Do not continue with an in-memory
or local-only session.

### Resume sandbox session

1. Host reads the session from its local store.
2. Host obtains the immutable target from `daw.execution.target`.
3. Host looks up the minimal sandbox lifecycle record.
4. Host resumes the sandbox and runner.
5. Runner calls `GetSession` through `RemoteStore` and reconstructs the runtime.
6. No sandbox-local history database is required.

### Listing and stored reads

Both operations query host SQLite only. They must work when:

- every sandbox is stopped;
- Docker Desktop/Sandboxes is unavailable;
- the corresponding sandbox has been explicitly removed, subject to the
  product's deletion policy;
- the dashboard has restarted.

## Adapter refactor

The end state should not merge host and sandbox session catalogs.

A minimal transition can replace `internal/adapter/hybrid` with a target router
(or substantially narrow it):

- `Info` and `ChatOptions`: host adapter;
- `ListSessions` and `ReadSession`: the one host-backed catalog;
- new `OpenChat`: choose host runtime or sandbox runtime from the request target;
- resume `OpenChat`: read `daw.execution.target` from the host store, then route;
- `Close`: close both execution backends, with the host store closed exactly
  once by its owner.

Rename it to something like `targetrouter` if useful; it is no longer a hybrid
catalog. `targetForSession` must not call both adapters' `ListSessions`.

A cleaner but larger refactor is to split the current broad `adapter.Adapter`
interface into:

```text
SessionCatalog   (list/read/delete)
ExecutionBackend (options/open/close)
```

That makes it impossible for the sandbox lifecycle adapter to accidentally
become a second catalog. This split is preferable if it can be done without
inflating the implementation.

The sandbox lifecycle index may still be needed for:

- session ID -> sandbox name;
- mounted working directories;
- template/image used by an existing sandbox;
- lifecycle state.

Remove `record.Summary` and duplicated session attributes from that index.
Authoritative title, timestamps, message counts, cost, lineage, working
location, and execution target belong in the host session store.

Normal host code should stop using these runner routes:

```text
GET /v1/sessions
GET /v1/sessions/{id}
```

They can be removed or retained for diagnostics, but they must not power the
dashboard catalog.

## Gossip and sub-sessions

This area needs explicit tests.

- `AddSubSession` must persist the child in the host store with the parent link.
- Child/root lineage attributes must survive RPC serialization.
- A child created through the host gossip callback must inherit the parent's
  immutable execution target before its runtime starts.
- If a gossip child receives its own sandbox, create its lifecycle mapping as
  part of the host-mediated child creation flow.
- Never infer a child's target by searching both execution backends.

## Failure semantics

The host store is intentionally a required dependency for sandbox execution.

- Host unavailable: fail the store operation and surface a run error; never
  fall back to local SQLite.
- Sandbox stops after a successful write: history is already durable on host.
- Timeout after an ambiguous mutation: use idempotency IDs or report the
  ambiguity without automatic duplication.
- Host shutdown: stop accepting new writes, allow in-flight requests to finish,
  stop runners/sandboxes, then close SQLite once.
- Runner shutdown: flush any explicitly designed buffer, close its HTTP client,
  and leave host storage open.
- Store protocol version mismatch: fail runner readiness with a clear error.

Add a health/capability handshake so the runner verifies the store API version
before accepting chat requests.

## Implementation sequence

1. Re-read the current cagent `session.Store` and its SQLite tests.
2. Add a contract-test suite that can run against any `session.Store`.
3. Implement the versioned host bridge over an in-memory store in tests.
4. Implement `RemoteStore` and run the same contract suite through HTTP.
5. Add body limits, error mapping, ordering, and idempotency behavior.
6. Allow `dagent.Adapter` to receive a caller-owned store.
7. Start the bridge in host DAW and pass its endpoint to sandboxrunner.
8. Make `cmd/daw-runner` require and use `RemoteStore`.
9. Persist execution target and lineage in the host store at session creation.
10. Change list/read/resume routing to use the host catalog only.
11. Reduce the sandbox index to lifecycle metadata.
12. Remove normal use of runner list/read routes and the merged catalog.
13. Add end-to-end tests, then delete the old sandbox-local storage and catalog
    compatibility code.

## Required tests

### Store contract

Run identical behavior tests against:

- cagent in-memory store;
- host SQLite store;
- `RemoteStore -> HTTP bridge -> in-memory store`;
- `RemoteStore -> HTTP bridge -> SQLite store`.

Cover every interface method, not just messages.

### Serialization fidelity

Round-trip a session containing:

- user and assistant messages;
- streaming `AddMessage`/`UpdateMessage` progression;
- reasoning and tool calls/results;
- image/document/attachment parts;
- summaries with model, usage, cost, and `FirstKeptEntry`;
- recorded errors;
- a sub-session;
- title/star/usage updates;
- parent/root/origin/execution-target attributes.

### Concurrency and reliability

- concurrent deltas under `go test -race`;
- ordered add/update operations;
- host-generated message IDs;
- duplicate operation-ID handling;
- cancellation and timeouts;
- bridge restart/host restart behavior;
- runner `Close` does not close host SQLite;
- failed host persistence prevents a successful run/open response.

### Bridge validation

- target attributes cannot be changed through payload data;
- oversized and malformed bodies are rejected;
- the store bridge is not exposed as part of the browser-facing API.

### DAW integration

- create Host and Docker Sandbox sessions and list both from one host store;
- stop all sandboxes and list/read every session with zero `sbx` calls;
- resume routes from persisted `daw.execution.target` after host restart;
- sandbox session writes remain visible after sandbox removal;
- gossip children retain lineage and target;
- plugin-selected execution locations survive create/resume;
- explicit deletion removes host history, lifecycle mapping, and sandbox
  according to the final deletion contract;
- bootstrap and chat options never provision a sandbox.

Use a fake/counting sandbox client to assert that list/read operations issue no
sandbox commands.

## Acceptance criteria

The work is complete when all of the following are true:

- There is exactly one authoritative durable session store, owned by host DAW.
- A sandbox runner uses a compile-time-checked remote implementation of the
  complete Docker Agent `session.Store` interface.
- New sandbox sessions do not create or depend on sandbox-local `session.db`.
- Session list and stored-session read work without Docker Sandboxes running and
  issue no `sbx` commands.
- The host does not merge two session catalogs.
- Resume routing uses the session's immutable host-stored execution target.
- The sandbox index contains lifecycle data only, not duplicate summaries.
- Stopping/removing a sandbox does not remove history.
- Host and sandbox runtimes preserve the same native session fidelity.
- `go test ./...`, `go vet ./...`, frontend tests, race tests for the new store,
  production builds, and end-to-end sandbox tests pass.

## Important cautions for the implementing agent

- Do not solve this by making the sandbox call the browser-facing DAW REST API.
- Do not send only normalized dashboard events and reconstruct a lossy session.
- Do not share/mount SQLite across host and sandbox processes.
- Do not retain `hybrid.ListSessions` as the normal catalog after host-backed
  storage is available.
- Do not close the shared host store once per chat or runner.
- Do not allow silent fallback to a sandbox-local DB.
- Do not make list/read boot a sandbox.
- Do not lose the execution-target, gossip-lineage, plugin-origin, or
  execution-location attributes while translating native sessions.
