# Targets used by contributors and CI. `make check` is what CI runs.

GO       ?= go
BIN      := bin/weightkeep
PKG      := github.com/afshinghezeli/weightkeep
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

export CGO_ENABLED := 0

.PHONY: build test test-race lint tidy-check check compat hooks clean

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/weightkeep

test:
	$(GO) test -timeout 5m ./...

# The race detector needs cgo.
test-race:
	CGO_ENABLED=1 $(GO) test -timeout 10m -race ./...

lint:
	golangci-lint run ./...

tidy-check:
	$(GO) mod tidy -diff

check: tidy-check lint test build

compat: build
	./test/compat/run.sh

hooks:
	git config core.hooksPath .githooks

clean:
	rm -rf bin dist
