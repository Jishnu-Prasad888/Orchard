SHELL := /bin/sh

BINARY      := bin/cairn
MODULE      := github.com/Jishnu-Prasad888/Cairn
VERSION     ?= dev
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DOCKER_TAG  ?= cairn:dev

RELEASE_DIR  := dist
PLATFORMS   ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

LDFLAGS := -s -w \
	-X "$(MODULE)/internal/version.Version=$(VERSION)" \
	-X "$(MODULE)/internal/version.Commit=$(COMMIT)" \
	-X "$(MODULE)/internal/version.BuildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)"

.PHONY: help build test test-go test-web web-install web-build web-dev dev reset-data gofmt lint \
	fmt fmt-check lint-go lint-web docker-build docker-buildx release release-cross clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' Makefile | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

reset-data: ## Delete the local dev data directory (DB, sessions, everything) for a clean slate
	rm -rf $(CURDIR)/cairn-data
	@echo "Removed $(CURDIR)/cairn-data"

build: reset-data web-build ## Build the production binary with the embedded frontend, wiping local dev data first
	@mkdir -p bin
	cp -r web/dist/. internal/webui/dist/
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/cairn
	@echo "Built $(BINARY)"

release: web-build ## Build release binaries for every platform in PLATFORMS
	@mkdir -p $(RELEASE_DIR)
	cp -r web/dist/. internal/webui/dist/
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out="$(RELEASE_DIR)/cairn-$$os-$$arch"; \
		[ "$$os" = "windows" ] && out="$$out.exe"; \
		echo "Building $$os/$$arch -> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o "$$out" ./cmd/cairn; \
	done
	shasum -a 256 $(RELEASE_DIR)/cairn-* > $(RELEASE_DIR)/SHA256SUMS
	@echo "Release artifacts written to $(RELEASE_DIR)/ (see SHA256SUMS)"

release-cross: ## Build release binaries without rebuilding the frontend (uses web/dist as-is)
	@mkdir -p $(RELEASE_DIR)
	cp -r web/dist/. internal/webui/dist/
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out="$(RELEASE_DIR)/cairn-$$os-$$arch"; \
		[ "$$os" = "windows" ] && out="$$out.exe"; \
		echo "Building $$os/$$arch -> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o "$$out" ./cmd/cairn; \
	done
	shasum -a 256 $(RELEASE_DIR)/cairn-* > $(RELEASE_DIR)/SHA256SUMS
	@echo "Cross-compiled binaries written to $(RELEASE_DIR)/ (see SHA256SUMS)"

dev: reset-data web-build ## Rebuild the frontend, wipe local dev data, and run the backend
	@mkdir -p $(CURDIR)/web/dist
	CAIRN_DATA_DIR=$(CURDIR)/cairn-data \
	CAIRN_HTTP_ADDR=127.0.0.1:8715 \
	CAIRN_WEB_DIST=$(CURDIR)/web/dist \
	go run ./cmd/cairn

web-install: ## Install frontend dependencies
	cd web && npm install

web-build: ## Build the frontend into web/dist
	cd web && npm run build

web-dev: ## Run the Vite dev server (proxies /api to :8715)
	cd web && npm run dev

test: test-go test-web ## Run all tests

test-go: ## Run backend tests
	go test -race ./cmd/... ./internal/...

qa: bin/cairn ## Run the Phase 19 end-to-end QA harness
	python3 qa/e2e.py

test-web: ## Run frontend tests
	cd web && npm test

lint: lint-go lint-web ## Run all linters

lint-go: ## Vet Go code and run golangci-lint if available
	go vet ./cmd/... ./internal/...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed; skipping (CI runs it)"; fi

lint-web: ## Lint the frontend
	cd web && npm run lint

fmt: ## Format Go and frontend code
	gofmt -w $$(find . -name '*.go' -not -path './web/*')
	cd web && npm run format

fmt-check: ## Fail if Go or frontend code is not formatted
	@if [ -n "$$(gofmt -l $$(find . -name '*.go' -not -path './web/*'))" ]; then \
		echo "gofmt: unformatted files:"; \
		gofmt -l $$(find . -name '*.go' -not -path './web/*'); \
		exit 1; \
	fi
	cd web && npm run format:check

docker-build: ## Build the Docker image for the current platform
	docker build -t $(DOCKER_TAG) .

docker-buildx: ## Build multi-arch Docker images (linux/arm64, linux/amd64)
	@if [ -z "$$CAIRN_PLATFORMS" ]; then echo "Set CAIRN_PLATFORMS, e.g. linux/arm64,linux/amd64"; exit 1; fi
	docker buildx build --platform "$$CAIRN_PLATFORMS" -t $(DOCKER_TAG) --push .

clean: ## Remove build artifacts
	rm -rf bin web/dist
	git clean -fd internal/webui/dist || true