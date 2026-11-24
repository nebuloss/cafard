# Two binaries: the server, with the web UIs inside, and the client, which
# carries none of it (cmd/mouchard imports internal/client alone).
SERVER := cafard
CLIENT := mouchard
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

# What a release is built for. The server runs as a Linux service
# (scripts/install.sh); the client runs on any machine next to the work,
# Windows and macOS included. amd64 and arm64 each; both are pure Go, so
# another platform is one more word here.
SERVER_PLATFORMS ?= linux/amd64 linux/arm64
CLIENT_PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# gofmt from the pinned toolchain, not whichever is first on PATH.
GOFMT = $(shell go env GOROOT)/bin/gofmt

.PHONY: help build dist repro unit web web-deps typecheck test-web test test-norace vet fmt fmt-check tidy-check check clean

help:
	@echo "make check        fmt-check, vet, tidy-check, typecheck, test-web, test: what CI runs"
	@echo "make build        $(DIST)/$(SERVER) with the web UIs, and $(DIST)/$(CLIENT)"
	@echo "make dist         every release platform in $(DIST)/release, with SHA256SUMS"
	@echo "make repro        build twice, apart, and fail unless the bytes match"
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
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/$(SERVER) ./cmd/$(SERVER)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/$(CLIENT) ./cmd/$(CLIENT)

# A release: one plain binary per binary and platform, and their checksums.
# Plain binaries rather than archives: the UIs are inside the server, so
# there is nothing else to ship.
dist: web
	@rm -rf $(DIST)/release && mkdir -p $(DIST)/release
	@set -e; for b in $(SERVER):"$(SERVER_PLATFORMS)" $(CLIENT):"$(CLIENT_PLATFORMS)"; do \
	  bin=$${b%%:*}; for p in $${b#*:}; do \
	    os=$${p%%/*}; arch=$${p##*/}; ext=; \
	    if [ "$$os" = windows ]; then ext=.exe; fi; \
	    echo "  $$bin $$os/$$arch"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	      go build $(GOFLAGS) -ldflags '$(LDFLAGS)' \
	      -o $(DIST)/release/$$bin-$$os-$$arch$$ext ./cmd/$$bin; \
	  done; \
	done
	@cd $(DIST)/release && sha256sum $(SERVER)-* $(CLIENT)-* > SHA256SUMS
	@# Every file shipped is in SHA256SUMS: v0.3.0's named the server's alone,
	@# and the setup scripts, which check mouchard against it, refused it.
	@cd $(DIST)/release && for f in $(SERVER)-* $(CLIENT)-*; do \
	  grep -q "  $$f$$" SHA256SUMS || { echo "$$f is not in SHA256SUMS"; exit 1; }; done
	@echo; ls -1 $(DIST)/release

# Two builds of the same commit must be the same bytes, or the checksums a
# release publishes cannot be checked by building it again; claudication's
# check. The second pass gets a copied tree, a cold build cache and the UIs
# built again, so a path, a timestamp or a cached artifact leaking into the
# output shows here.
repro: web
	@rm -rf $(DIST)/repro && mkdir -p $(DIST)/repro
	@echo "pass 1"
	@CGO_ENABLED=0 SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
	  go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/repro/first ./cmd/$(SERVER)
	@echo "pass 2 (cold cache, copied tree, UIs built again)"
	@rm -rf $(DIST)/repro/tree && mkdir -p $(DIST)/repro/tree
	@tar -c --exclude='./$(DIST)' --exclude='./web/node_modules' --exclude='./.git' \
	  --exclude='./web/dist' --exclude='./$(WEBDIST)' . \
	  | tar -x -C $(DIST)/repro/tree
	@mkdir -p $(DIST)/repro/tree/$(WEBDIST) && touch $(DIST)/repro/tree/$(WEBDIST)/.gitkeep
	@ln -s $(CURDIR)/web/node_modules $(DIST)/repro/tree/web/node_modules
	@$(MAKE) --no-print-directory -C $(DIST)/repro/tree web VERSION=$(VERSION) COMMIT=$(COMMIT) >/dev/null
	@cd $(DIST)/repro/tree && CGO_ENABLED=0 GOCACHE=$(CURDIR)/$(DIST)/repro/cache \
	  go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(CURDIR)/$(DIST)/repro/second ./cmd/$(SERVER)
	@if ! diff -r $(WEBDIST) $(DIST)/repro/tree/$(WEBDIST) >/dev/null; then \
	  echo; echo "NOT reproducible: the two UI builds differ"; \
	  diff -rq $(WEBDIST) $(DIST)/repro/tree/$(WEBDIST); exit 1; \
	fi
	@if cmp -s $(DIST)/repro/first $(DIST)/repro/second; then \
	  echo; echo "reproducible: $$(sha256sum $(DIST)/repro/first | cut -d' ' -f1)"; \
	else \
	  echo; echo "NOT reproducible: the two builds differ"; \
	  ls -l $(DIST)/repro/first $(DIST)/repro/second; exit 1; \
	fi

# deploy/cafard.service is what scripts/install.sh writes, generated from it
# so the two cannot drift: run this after changing the unit in the script.
unit:
	env -i PATH="$$PATH" INSTALL_SH_SOURCED=1 sh -c '. scripts/install.sh; systemd_unit' > deploy/cafard.service

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
