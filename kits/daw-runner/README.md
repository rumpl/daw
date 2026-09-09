# Atelier sandbox runner kit

Atelier uses two immutable OCI artifacts:

1. a **template image** derived from Docker's generic
   `docker/sandbox-templates:shell-docker` image, with `daw-runner` baked into
   `/home/agent/.local/lib/daw-runner`; and
2. a small schema-v2 **sandbox kit** that defines Atelier's runner, selects
   that template, declares credentials/resources, and uses `/bin/true` as the
   sandbox-managed entrypoint.

This split is intentional. The runner is roughly 100 MB. Static kit files are
materialized into every newly-created sandbox, while template image layers are
pulled once by the sandbox runtime and cached across sandbox creation and
deletion. Baking the runner into the template removes the dominant per-session
startup cost.

The generic Docker-enabled base is deliberate: Atelier's runner embeds the
Docker Agent SDK, so it does not need the separate Docker Agent CLI binary from
an agent-specific template. `shell-docker` retains the isolated Docker Engine
used by coding tools without shipping that duplicate layer.

The host starts the runner after `sbx run` completes using an authenticated,
long-lived `sbx exec` stdio stream. This ensures the process inherits the fully
initialized credential-proxy environment. No sandbox port is published.

## Build and publish

Publish both content-addressed artifacts with:

```sh
make publish-sandbox-kit
```

The target:

1. cross-compiles and strips the Linux runner;
2. hashes the Dockerfile, spec, and runner;
3. builds and pushes `daw-runner-template:<hash>`;
4. substitutes that immutable template reference into the runtime kit spec;
5. validates and pushes `daw-runner-kit:<hash>`; and
6. writes the resulting kit reference to `bin/daw-runner-kit.ref`.

Override the repositories when needed:

```sh
make publish-sandbox-kit \
  RUNNER_TEMPLATE_REPOSITORY=docker.io/example/daw-runner-template \
  RUNNER_KIT_REPOSITORY=docker.io/example/daw-runner-kit
```

Docker Sandboxes has a separate image store from the host Docker daemon. The
first sandbox using a new template pulls it; subsequent sandboxes reuse the
cached image. For local-only iteration, Docker's documentation also supports
`docker image save` followed by `sbx template load`.

## Why not `setup.install` or `files/`?

Docker's kit specification says install commands run at sandbox creation and
static kit files are injected into each sandbox. Both are suitable for small
configuration, but not for a large executable on the startup-critical path.
Templates are explicitly the build-once/reuse mechanism for heavy, stable
content.

`setup.startup` is also unsuitable: startup commands run on every start and do
not gate the agent entrypoint. Atelier instead starts the runner explicitly
after sandbox initialization.

## Session lifecycle

For every sandbox-targeted session, Atelier:

1. creates a dedicated sandbox on the first message;
2. mounts the logical workspace and any selected sibling execution directory;
3. starts `daw-runner` over authenticated stdio;
4. stops the sandbox when no live chat owns the session; and
5. restarts the same sandbox and reconnects on resume.

Existing sandboxes are reattached by name, so their filesystem and Docker cache
remain warm. Complete chat history stays in the host Docker Agent session store;
the host stores only lifecycle mappings under `~/.cagent/dawui/`.

Model credentials remain in the host-side `sbx secret` store. The kit declares
proxy-managed bindings, and real values never enter the VM. Docker gateway login
support is composed as a small per-session mixin when required.
