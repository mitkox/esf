# Factory development entry points.
#
# The upstream Machinist project uses `just` (see Justfile); this Makefile adds
# the factory-specific workflow on top and does not replace it.
#
# Everything here is runnable from a clean checkout on this host.

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
UV ?= uv
PYTHON_VERSION ?= 3.14.7

BIN_DIR    := bin
FACTORY    := $(BIN_DIR)/factory
TEMPORAL_DIR := deployments/dev/temporal
COMPOSE    := docker compose --project-directory $(TEMPORAL_DIR)

# Set Cube connection variables in your environment for live integration work.
# A separately installed agent path is needed only by the legacy spike target.
OPENCODE_BINARY ?= /opt/esf/agents/opencode2

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ── Build and test ──────────────────────────────────────────────────────────

.PHONY: build
build: ## Build the factory and discovery binaries into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(FACTORY) ./cmd/factory
	$(GO) build -o $(BIN_DIR)/cube-smoke ./tools/cube-smoke
	$(GO) build -o $(BIN_DIR)/cube-netprobe ./tools/cube-netprobe
	$(GO) build -o $(BIN_DIR)/cube-agent-spike ./tools/cube-agent-spike
	@echo "built: $(FACTORY) $(BIN_DIR)/cube-smoke $(BIN_DIR)/cube-netprobe $(BIN_DIR)/cube-agent-spike"

.PHONY: test
test: ## Run Go, frontend, and optional Python unit tests
	$(GO) test ./... -count=1
	$(MAKE) test-frontend test-python

.PHONY: frontend test-frontend test-python
frontend: ## Install frozen frontend dependencies and build the UI
	cd internal/controlplane/web && npm ci && npm run build

test-frontend: ## Run frontend tests against the frozen lockfile
	cd internal/controlplane/web && npm ci && npm test

test-python: ## Run optional Python tool tests against uv.lock
	@if command -v $(UV) >/dev/null && $(UV) python find $(PYTHON_VERSION) >/dev/null 2>&1; then \
		$(UV) run --locked --python $(PYTHON_VERSION) python -m unittest discover -s tools/intake/tests -p 'test_*.py' && \
		$(UV) run --locked --python $(PYTHON_VERSION) python -m unittest discover -s tools/brief_lab/tests -p 'test_*.py'; \
	else \
		$(MAKE) test-python-docker; \
	fi

.PHONY: test-python-docker
test-python-docker: ## Test optional Python tools in the pinned 3.14.7 image
	docker build -f deploy/images/Dockerfile.intake --build-arg ESF_COMMIT=$$(git rev-parse HEAD) -t esf/intake:python-tests .
	docker run --rm --entrypoint /opt/esf/.venv/bin/python esf/intake:python-tests -c 'import sys; assert sys.version_info[:3] == (3, 14, 7)'
	docker run --rm --entrypoint /opt/esf/.venv/bin/python esf/intake:python-tests -m unittest discover -s tools/intake/tests -p 'test_*.py'
	docker run --rm --entrypoint /opt/esf/.venv/bin/python esf/intake:python-tests -m unittest discover -s tools/brief_lab/tests -p 'test_*.py'

.PHONY: test-unit
test-unit: ## Run unit tests only (fast)
	$(GO) test ./internal/... ./cmd/... -count=1

.PHONY: test-integration
test-integration: ## Run tests that need the live CubeSandbox (tagged integration)
	$(GO) test -tags=integration ./... -count=1 -v

.PHONY: lint
lint: ## Check Go formatting, vet, and shell scripts without changing files
	@out=$$(find cmd internal -name '*.go' -print0 | xargs -0 gofmt -l); if [ -n "$$out" ]; then echo "unformatted Go files:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	shellcheck deployments/dev/temporal/scripts/*.sh scripts/*.sh

.PHONY: fmt
fmt: ## Format all Go code
	$(GO) fmt ./...

# ── Temporal ────────────────────────────────────────────────────────────────

.PHONY: temporal-up
temporal-up: ## Start the Temporal development stack and wait for health
	$(COMPOSE) up -d
	./$(TEMPORAL_DIR)/scripts/wait-ready.sh
	@echo "Temporal gRPC: 127.0.0.1:7233"
	@echo "Temporal UI:   http://127.0.0.1:8233"

.PHONY: temporal-down
temporal-down: ## Stop the Temporal development stack (data volume preserved)
	$(COMPOSE) down

.PHONY: temporal-clean
temporal-clean: ## Stop Temporal AND delete its data volume
	$(COMPOSE) down -v

.PHONY: temporal-status
temporal-status: ## Show Temporal container and namespace status
	@$(COMPOSE) ps
	@echo
	@docker exec factory-temporal-create-namespace \
		temporal operator namespace list --address temporal:7233 2>/dev/null \
		|| echo "(namespace listing unavailable)"

.PHONY: temporal-logs
temporal-logs: ## Tail Temporal server logs
	$(COMPOSE) logs -f temporal

.PHONY: temporal-hello
temporal-hello: ## Prove a workflow can execute end to end
	$(GO) test -tags=integration ./internal/factory -run '^TestTemporalHelloWorkflow$$' -count=1 -v

# ── CubeSandbox ─────────────────────────────────────────────────────────────

.PHONY: cube-smoke
cube-smoke: ## Prove the factory host can use the existing CubeSandbox
	$(GO) run ./tools/cube-smoke

.PHONY: cube-netprobe
cube-netprobe: ## Discover the effective egress policy of this deployment
	$(GO) run ./tools/cube-netprobe

.PHONY: cube-agent-spike
cube-agent-spike: ## Prove the coding agent runs inside a Cube microVM
	$(GO) run ./tools/cube-agent-spike -e2e -opencode-binary $(OPENCODE_BINARY)

.PHONY: cube-sandboxes
cube-sandboxes: build ## List live CubeSandboxes (leak check)
	$(FACTORY) sandboxes

# ── Factory ─────────────────────────────────────────────────────────────────

.PHONY: factory-doctor
factory-doctor: build ## Check configuration, Cube and Temporal
	$(FACTORY) doctor

.PHONY: factory-worker
factory-worker: build ## Run the Temporal worker in the foreground
	$(FACTORY) worker

.PHONY: e2e-repo
e2e-repo: ## Materialise the disposable E2E fixture repository
	./scripts/make-e2e-repo.sh

.PHONY: factory-run-demo
factory-run-demo: build e2e-repo ## Run the acceptance test: one task in, one verified patch out
	./scripts/factory-run-demo.sh

.PHONY: integration-test
integration-test: cube-smoke temporal-up ## Run the full integration suite
	$(GO) test -tags=integration ./internal/... -count=1 -v

.PHONY: verify
verify: lint test cube-smoke ## Run everything that does not need Temporal

.PHONY: clean
clean: ## Remove build output and local factory state
	rm -rf $(BIN_DIR) .factory
