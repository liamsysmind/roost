# roost — build automation.
#
# `make` produces a stripped release binary for the host platform.
# `make cross` produces binaries for Linux/macOS host targets in dist/.

VERSION  ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GO       ?= go

# vendor/ is gitignored but required at build time (//go:embed all:web bakes
# it into the binary). xterm.js doubles as the stamp file — if it's present,
# assume the rest of the bundle is too.
WEB_VENDOR   := internal/server/web/vendor
VENDOR_STAMP := $(WEB_VENDOR)/xterm.js

.PHONY: all build run dev test fmt vet cross clean install vendor

all: build

# Fetch xterm.js + JS deps. Auto-runs on first `make build`; re-run to refresh
# after bumping pins in scripts/fetch-vendor.sh.
vendor: $(VENDOR_STAMP)

$(VENDOR_STAMP):
	@./scripts/fetch-vendor.sh

# Standard local build → ./roost
build: $(VENDOR_STAMP)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o roost ./cmd/roost

# Fast iteration: rebuild and restart the local server.
dev: build
	pkill -x roost 2>/dev/null || true
	./roost serve

run: build
	./roost serve

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

# Cross-compile to the three target tuples we care about.
# Output ends up in dist/.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64

cross: $(VENDOR_STAMP)
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		out="dist/roost-$(VERSION)-$$os-$$arch"; \
		echo "→ $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o "$$out" ./cmd/roost || exit 1; \
	done
	@ls -lh dist/

clean:
	rm -f roost
	rm -rf dist/

# Install to /usr/local/bin. Needs sudo.
install: build
	install -m 0755 roost /usr/local/bin/roost
	@echo "Installed to /usr/local/bin/roost"
