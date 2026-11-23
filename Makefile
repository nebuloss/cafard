BINARY := cafard
CMD    := ./cmd/cafard
DIST   := dist
PKG    := cafard/internal/version

# Pin the compiler to the exact version go.mod asks for, as claudication does:
# the `go` directive is only a floor, and two compiler versions produce two
# different binaries from identical source.
GOVERSION := $(shell awk '/^go /{print $$2; exit}' go.mod)
export GOTOOLCHAIN := go$(GOVERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

# The build date comes from the commit, not from the clock, so two builds of
# the same source are the same bytes.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
DATE ?= $(shell date -u -d "@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
                || date -u -r "$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
                || echo unknown)

LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
GOFLAGS := -trimpath -buildvcs=false

# gofmt from the pinned toolchain, not whichever is first on PATH.
GOFMT = $(shell go env GOROOT)/bin/gofmt

.PHONY: help build web web-deps typecheck test-web test test-norace vet fmt fmt-check tidy-check check clean

help:
	@echo "make check        fmt-check, vet, tidy-check, typecheck, test-web, test: what CI runs"
	@echo "make build        $(DIST)/$(BINARY), static, with the web UIs (make web first)"
	@echo "make web          build the web UIs into internal/webui/dist, where go:embed takes them"
	@echo "make typecheck    tsc over web/: Vite strips types without checking them"
	@echo "make test-web     the web UIs' unit tests, under node"
	@echo "make test         Go tests with -race (needs cgo)"
	@echo "make test-norace  the same without cgo"
	@echo "make fmt          gofmt every Go file"

# The web UIs are TypeScript in web/, built by Vite and embedded by
# internal/webui from internal/webui/dist, which git holds empty. npm ci runs
# when node_modules is missing or older than the lockfile, so the compilers
# are always the lockfile's.
WEBDIST := internal/webui/dist

web/node_modules/.package-lock.json: web/package-lock.json
	cd web && npm ci --no-audit --no-fund --silent

web-deps: web/node_modules/.package-lock.json

web: web-deps
	cd web && npm run build --silent
	@find $(WEBDIST) -mindepth 1 ! -name .gitkeep -delete
	@cp -r web/dist/. $(WEBDIST)/

typecheck: web-deps
	cd web && npm run typecheck --silent

test-web: web-deps
	cd web && npm test --silent

build: web
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY) $(CMD)

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

test-norace:
	CGO_ENABLED=0 go test -count=1 ./...

vet:
	go vet ./...

fmt:
	$(GOFMT) -w $$(git ls-files '*.go')

fmt-check:
	@out=$$($(GOFMT) -l $$(git ls-files '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy-check:
	go mod tidy -diff

check: fmt-check vet tidy-check typecheck test-web test

clean:
	rm -rf $(DIST) web/dist
	@find $(WEBDIST) -mindepth 1 ! -name .gitkeep -delete
