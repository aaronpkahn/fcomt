SHELL := /bin/bash
.DEFAULT_GOAL := help

APP_ADDR ?= 127.0.0.1:8080
BASE_URL ?= http://$(APP_ADDR)
SCENARIO ?= local_only_weekly_timeseries
GO_CACHE_DIR ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE_DIR ?= $(CURDIR)/.cache/go-mod
GO_ENV = GOCACHE=$(GO_CACHE_DIR) GOMODCACHE=$(GO_MOD_CACHE_DIR)

.PHONY: help cache run build bin test report install nosleep examples seed ui demo

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

cache: ## Create local Go cache directories used by make targets
	@mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"

run: cache ## Run the agent API + local visualizer
	@$(GO_ENV) go run ./cmd/fcomt-agent

build: cache bin ## Build the project (also produces bin/fcomt-agent)
	@$(GO_ENV) go build ./...

bin: cache ## Build the agent binary at bin/fcomt-agent
	@mkdir -p bin
	@$(GO_ENV) go build -o bin/fcomt-agent ./cmd/fcomt-agent

report: bin ## Print today's outage report from the DB (DATE=YYYY-MM-DD optional)
	@./bin/fcomt-agent -report daily $(if $(DATE),-date $(DATE))

install: bin ## Install + load the launchd agent (deploy/install.sh)
	@./deploy/install.sh

nosleep: ## Disable system sleep (sudo; deploy/nosleep.sh)
	@./deploy/nosleep.sh

test: cache ## Run unit tests
	@$(GO_ENV) go test ./...

examples: ## List built-in example scenarios from the running agent
	@curl --fail --silent --show-error "$(BASE_URL)/v1/examples"
	@echo

seed: ## Seed SCENARIO into the running agent (e.g. make seed SCENARIO=server_assisted_high_granularity)
	@curl --fail --silent --show-error -X POST "$(BASE_URL)/v1/examples/seed?name=$(SCENARIO)"
	@echo

ui: ## Print local visualizer URL
	@echo "$(BASE_URL)/ui/"

demo: cache ## Start app, wait for readiness, seed SCENARIO, and keep app running
	@set -euo pipefail; \
	$(GO_ENV) go run ./cmd/fcomt-agent & \
	pid=$$!; \
	trap 'kill $$pid 2>/dev/null || true' EXIT INT TERM; \
	for i in {1..60}; do \
		if curl -fsS "$(BASE_URL)/healthz" >/dev/null 2>&1; then \
			break; \
		fi; \
		sleep 0.25; \
	done; \
	curl --fail --silent --show-error -X POST "$(BASE_URL)/v1/examples/seed?name=$(SCENARIO)" >/dev/null; \
	echo "Seeded scenario: $(SCENARIO)"; \
	echo "Visualizer: $(BASE_URL)/ui/"; \
	echo "Press Ctrl-C to stop."; \
	wait $$pid
