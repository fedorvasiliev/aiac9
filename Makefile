APP_NAME    := aiac9
MODULE      := github.com/fedorvasiliev/aiac9
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X $(MODULE)/internal/version.Version=$(VERSION) \
               -X $(MODULE)/internal/version.Commit=$(COMMIT) \
               -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build run test lint fmt vet tidy clean

build: ## Build the binary into ./bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/$(APP_NAME)

run: ## Run the HTTP server locally
	go run ./cmd/$(APP_NAME) serve

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: vet ## Alias for vet (swap in golangci-lint later if desired)

fmt: ## Format all Go source files
	gofmt -l -w .

tidy: ## Sync go.mod/go.sum with imports
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin
