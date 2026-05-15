.PHONY: all build test clean run dev help

# Variables
BINARY=bin/browserctl-svc
GO=go
GOFLAGS=-ldflags="-s -w"

# Default target
all: test build

# Build the binary
build:
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/server

# Run tests
test:
	$(GO) test -v ./...

# Run tests with coverage
cover:
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run the service (dev mode)
run: build
	./$(BINARY)

# Run with custom env
dev:
	./$(BINARY)

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Build for all platforms
build-all:
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o bin/browserctl-svc-linux-amd64 ./cmd/server
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o bin/browserctl-svc-darwin-amd64 ./cmd/server

# Lint
lint:
	$(GO) vet ./...
	golangci-lint run 2>/dev/null || true

# Generate mocks (if needed)
generate:
	$(GO) generate ./...