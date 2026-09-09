# Developing Atelier

[Back to the Atelier overview](../README.md)

```bash
make generate    # regenerate web/src/protocol.gen.ts from the Go types
make typecheck   # go vet, staticcheck, tsc --noEmit
make test        # Go tests + Vitest
make test-e2e    # Playwright (Chromium)
make screenshots # render desktop + mobile shots to /tmp/uishots
```

Tests run against the fake adapter, so they never spend model tokens, pull an
image or start a sandbox. They cover path containment, session ownership, event
normalisation, SSE reconnect and replay, CSRF and origin checks, and Markdown
safety.

`scripts/smoke-real.sh` exercises a real model end to end. It asks for
confirmation first and spends tokens.
