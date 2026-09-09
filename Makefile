# docker-agent web dashboard
#
# The task names mirror the docker-agent checkout's Taskfile.yml conventions
# (dev / typecheck / test / build) so muscle memory carries over.

SHELL := /bin/bash
BIN := bin/dawui
SANDBOX_BIN := bin/daw-sandbox
RUNNER_KIT_BIN := kits/daw-runner/files/home/.local/lib/daw-runner
RUNNER_REPOSITORY ?= docker.io/djordjelukic1639080/daw-runner
RUNNER_TEMPLATE_REPOSITORY ?= $(RUNNER_REPOSITORY)-template
RUNNER_KIT_REPOSITORY ?= $(RUNNER_REPOSITORY)-kit
RUNNER_KIT_REF := bin/daw-runner-kit.ref
GO ?= go
WEB_DEPS_STAMP := web/node_modules/.daw-installed

.PHONY: all deps electron-deps generate dev dev-fake typecheck lint test test-go test-race test-web test-e2e \
        ci build build-web build-go build-sandbox-launcher build-runner-kit publish-sandbox-kit electron package-electron clean smoke-real screenshots help

all: build

## deps: install frontend dependencies from the committed lockfile
deps: $(WEB_DEPS_STAMP)

$(WEB_DEPS_STAMP): web/package.json web/package-lock.json
	cd web && npm ci
	@touch $@

## electron-deps: install the desktop host and its native Electron runtime
electron-deps:
	cd electron && npm ci
	# npm may have ignore-scripts=true (for example under Socket Firewall),
	# which skips Electron's required binary download. Run it explicitly.
	cd electron && ELECTRON_SKIP_BINARY_DOWNLOAD= node node_modules/electron/install.js

## generate: regenerate the TypeScript protocol mirror from the Go types
generate:
	$(GO) run ./cmd/tsgen web/src/protocol.gen.ts

## dev: run the sandbox-enabled Go backend and open the Vite dev UI
# The backend deliberately runs without embedded web assets: Vite owns the UI
# during development and proxies /api to it.
dev: generate deps publish-sandbox-kit
	@echo "API:  http://127.0.0.1:4788"
	@echo "UI :  http://127.0.0.1:4789  (proxies /api to the Go server)"
	@trap 'kill 0' EXIT INT TERM; \
	workspace=$$(cd "$${WORKSPACE:-.}" && pwd -P); \
	PORT=4788 \
		DAWUI_SANDBOX_PER_SESSION=1 \
		DAWUI_SANDBOX_WORKSPACE="$$workspace" \
		DAWUI_SANDBOX_KIT="$$(cat $(RUNNER_KIT_REF))" \
		$(GO) run ./cmd/dawui & \
	cd web && npm run dev -- --open & \
	wait

## dev-fake: run without a sandbox using the deterministic test adapter
dev-fake: generate deps
	@trap 'kill 0' EXIT INT TERM; \
	PORT=4788 DAWUI_FAKE_ADAPTER=1 DAWUI_FAKE_DELAY_MS=40 $(GO) run ./cmd/dawui & \
	cd web && npm run dev -- --open & \
	wait

## typecheck: go vet, staticcheck when available, and tsc --noEmit
typecheck:
	$(GO) vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "staticcheck ./..."; \
		out=$$(staticcheck ./... 2>&1); rc=$$?; \
		if [ $$rc -ne 0 ] && echo "$$out" | grep -qE "built with go|requires newer Go version"; then \
			echo "WARNING: the installed staticcheck predates this module's Go toolchain (go 1.26.5) and cannot analyse it."; \
			echo "         Reinstall with: go install honnef.co/go/tools/cmd/staticcheck@latest"; \
		elif [ $$rc -ne 0 ]; then \
			echo "$$out"; exit $$rc; \
		else \
			echo "$$out"; \
		fi; \
	else \
		echo "staticcheck not installed; skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi
	cd web && npx tsc --noEmit

## lint: run the Go linter configuration
lint:
	golangci-lint run ./...

## test: Go tests and Vitest
test: test-go test-web

test-go:
	$(GO) test ./...

## test-race: run Go tests with the race detector
test-race:
	$(GO) test -race ./...

test-web:
	cd web && npx vitest run

## test-e2e: Playwright (Chromium only), against the production binary + fake adapter
test-e2e: build
	cd e2e && npm ci 2>/dev/null || (cd e2e && npm install)
	cd e2e && npx playwright install chromium
	cd e2e && npx playwright test

## ci: run the complete pull-request gate after dependencies are installed
ci: generate
	git diff --exit-code -- web/src/protocol.gen.ts
	$(MAKE) lint
	$(MAKE) typecheck
	$(MAKE) test-race
	$(MAKE) test-web
	$(MAKE) build

## build: compile the app and publish its content-addressed sandbox kit
build: generate build-web build-go publish-sandbox-kit

build-web: deps
	cd web && npm run build

# Stamp the docker-agent module version the build actually resolved, so
# /api/bootstrap reports the truth instead of the library's build-time default
# ("dev"). A released module has no git checkout, so no commit is stamped.
CAGENT_VERSION := $(shell $(GO) list -m -f '{{.Version}}' github.com/docker/docker-agent)

build-go:
	mkdir -p bin
	$(GO) build -tags webassets -trimpath -ldflags "\
	  -X main.appVersion=$$(git describe --tags --always 2>/dev/null || echo dev) \
	  -X github.com/docker/docker-agent/pkg/version.Version=$(CAGENT_VERSION)" \
	  -o $(BIN) ./cmd/dawui

## build-sandbox-launcher: compile the host-side per-session sandbox launcher
build-sandbox-launcher:
	mkdir -p $(dir $(SANDBOX_BIN))
	$(GO) build -trimpath -o $(SANDBOX_BIN) ./cmd/daw-sandbox

## build-runner-kit: cross-compile and validate the code-defined Linux sandbox runner
build-runner-kit:
	mkdir -p $(dir $(RUNNER_KIT_BIN))
	CGO_ENABLED=0 GOOS=linux GOARCH=$$($(GO) env GOARCH) $(GO) build -trimpath -ldflags "\
	  -s -w \
	  -X main.appVersion=$$(git describe --tags --always 2>/dev/null || echo dev) \
	  -X github.com/docker/docker-agent/pkg/version.Version=$(CAGENT_VERSION)" \
	  -o $(RUNNER_KIT_BIN) ./cmd/daw-runner

## publish-sandbox-kit: bake the runner into a cached template and publish its small configuration kit
publish-sandbox-kit: build-runner-kit
	@mkdir -p $(dir $(RUNNER_KIT_REF)); \
	 digest=$$(find kits/daw-runner/spec.yaml kits/daw-runner/Dockerfile $(RUNNER_KIT_BIN) -type f | LC_ALL=C sort | xargs shasum -a 256 | shasum -a 256 | cut -c1-12); \
	 template_ref="$(RUNNER_TEMPLATE_REPOSITORY):$$digest"; \
	 kit_ref="$(RUNNER_KIT_REPOSITORY):$$digest"; \
	 echo "publishing sandbox template $$template_ref"; \
	 docker buildx build --platform linux/$$($(GO) env GOARCH) --push -t "$$template_ref" -f kits/daw-runner/Dockerfile kits/daw-runner; \
	 stage=$$(mktemp -d); \
	 trap 'rm -rf "$$stage"' EXIT; \
	 sed "s|__DAW_RUNNER_TEMPLATE__|$$template_ref|" kits/daw-runner/spec.yaml > "$$stage/spec.yaml"; \
	 sbx kit validate "$$stage"; \
	 echo "publishing sandbox kit $$kit_ref"; \
	 sbx kit push "$$stage" "$$kit_ref"; \
	 printf '%s\n' "$$kit_ref" > $(RUNNER_KIT_REF)

## electron: build and launch the sandbox-first Electron desktop app (backend uses a UDS)
electron: build build-sandbox-launcher electron-deps
	cd electron && npm start

## package-electron: create a sandbox-first native Electron artifact in electron/dist
package-electron: build build-sandbox-launcher electron-deps
	cd electron && npm run dist

## screenshots: capture desktop + mobile UI screenshots against the fake adapter
screenshots: build
	@pkill -f 'bin/dawui' 2>/dev/null || true
	@PORT=4797 DAWUI_FAKE_ADAPTER=1 DAWUI_FAKE_DELAY_MS=30 ./$(BIN) & \
	 sleep 2; cd e2e && node screenshot.mjs; kill %1 2>/dev/null || true
	@echo "wrote /tmp/uishots/desktop.png and /tmp/uishots/mobile.png"

## smoke-real: OPT-IN only. Sends one real prompt to a real model and spends tokens.
smoke-real:
	./scripts/smoke-real.sh

clean:
	rm -rf bin internal/webassets/dist web/node_modules e2e/node_modules electron/node_modules electron/dist
	rm -f $(SANDBOX_BIN) $(RUNNER_KIT_BIN) $(RUNNER_KIT_REF)

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
