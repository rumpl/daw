# docker-agent web dashboard
#
# The task names mirror the docker-agent checkout's Taskfile.yml conventions
# (dev / typecheck / test / build) so muscle memory carries over.

SHELL := /bin/bash
BIN := bin/dawui
RUNNER_KIT_BIN := kits/daw-runner/files/home/.local/lib/daw-runner
PORT ?= 4788
GO ?= go

.PHONY: all deps electron-deps generate dev dev-fake typecheck lint test test-go test-race test-web test-e2e \
        ci build build-web build-go build-runner-kit start start-sandbox electron package-electron clean smoke-real screenshots help

all: build

## deps: install frontend dependencies from the committed lockfile
deps:
	cd web && npm ci || npm install

## electron-deps: install the desktop host and its native Electron runtime
electron-deps:
	cd electron && npm ci
	# npm may have ignore-scripts=true (for example under Socket Firewall),
	# which skips Electron's required binary download. Run it explicitly.
	cd electron && ELECTRON_SKIP_BINARY_DOWNLOAD= node node_modules/electron/install.js

## generate: regenerate the TypeScript protocol mirror from the Go types
generate:
	$(GO) run ./cmd/tsgen web/src/protocol.gen.ts

## dev: run the Go server and the Vite dev server together
dev: generate
	@echo "API:  http://127.0.0.1:$(PORT)"
	@echo "UI :  http://127.0.0.1:4789  (proxies /api to the Go server)"
	@trap 'kill 0' EXIT INT TERM; \
	PORT=$(PORT) $(GO) run ./cmd/dawui & \
	cd web && npm run dev & \
	wait

## dev-fake: same as dev but with the deterministic fake docker-agent adapter
dev-fake: generate
	@trap 'kill 0' EXIT INT TERM; \
	PORT=$(PORT) DAWUI_FAKE_ADAPTER=1 DAWUI_FAKE_DELAY_MS=40 $(GO) run ./cmd/dawui & \
	cd web && npm run dev & \
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

## build: compile the Go binary with the frontend embedded
build: generate build-web build-go

build-web:
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

## build-runner-kit: cross-compile the code-defined Linux runner into the local sandbox kit
build-runner-kit:
	mkdir -p $(dir $(RUNNER_KIT_BIN))
	CGO_ENABLED=0 GOOS=linux GOARCH=$$($(GO) env GOARCH) $(GO) build -trimpath -ldflags "\
	  -X main.appVersion=$$(git describe --tags --always 2>/dev/null || echo dev) \
	  -X github.com/docker/docker-agent/pkg/version.Version=$(CAGENT_VERSION)" \
	  -o $(RUNNER_KIT_BIN) ./cmd/daw-runner

## start: run the compiled production binary
start: $(BIN)
	PORT=$(PORT) ./$(BIN)

## start-sandbox: build and create a sandbox containing the code-defined runner
start-sandbox: build build-runner-kit
	$(GO) run ./cmd/daw-sandbox -workspace "$${WORKSPACE:-.}" -dashboard ./$(BIN)

$(BIN):
	$(MAKE) build

## electron: build and launch the Electron desktop app (backend uses a UDS)
electron: build electron-deps
	cd electron && npm start

## package-electron: create a native Electron artifact in electron/dist
package-electron: build electron-deps
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
	rm -f $(RUNNER_KIT_BIN)

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
