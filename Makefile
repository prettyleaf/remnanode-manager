VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev-$$(git rev-parse --short HEAD 2>/dev/null))
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BIN := remnanode-manager
BI := github.com/prettyleaf/remnanode-manager/internal/buildinfo
LDFLAGS := -s -w -X $(BI).Version=$(VERSION) -X $(BI).Commit=$(COMMIT) -X $(BI).Branch=$(BRANCH) -X $(BI).BuildTime=$(BUILD_TIME)

.PHONY: web build run dev-web test lint

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/remnanode-manager

run:
	DATA_DIR=./data go run -ldflags "$(LDFLAGS)" ./cmd/remnanode-manager

dev-web:
	cd web && pnpm dev

test:
	go vet ./...
	go test ./...

lint:
	cd web && pnpm typecheck && pnpm lint
