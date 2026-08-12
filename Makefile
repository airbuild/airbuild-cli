VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -X main.version=$(VERSION)
BINARY   = airbuild
DIST     = dist

# Target platforms for cross-compilation.
TARGETS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: all build test clean dist install

## Build the binary for the current platform.
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## Build for all target platforms into dist/.
dist: clean
	@mkdir -p $(DIST)
	@for target in $(TARGETS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		ext=""; \
		[ $$os = windows ] && ext=".exe"; \
		echo "  → $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -trimpath \
			-o $(DIST)/$(BINARY)-$$os-$$arch$$ext .; \
	done
	@echo "Done. Binaries in $(DIST)/"

## Install to $GOBIN.
install:
	go install -ldflags "$(LDFLAGS)" .

## Run tests.
test:
	go test ./...

## Tidy dependencies.
tidy:
	go mod tidy

## Clean build artifacts.
clean:
	rm -rf $(BINARY) $(DIST)

## Show available targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
