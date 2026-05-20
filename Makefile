BINARY    := sem-ai
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
INSTALL   := /usr/local/bin

.PHONY: build install uninstall clean test fmt vet release

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install: build
	cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "installed $(INSTALL)/$(BINARY) ($(VERSION))"

uninstall:
	rm -f $(INSTALL)/$(BINARY)
	@echo "removed $(INSTALL)/$(BINARY)"

clean:
	rm -f $(BINARY)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

# Bump plugin manifests, commit, and tag a new release.
#   Usage: make release VERSION=0.1.8
# Does NOT push — review locally, then run the printed git push commands.
release:
	@test -n "$(VERSION)" || (echo "ERROR: VERSION required (e.g. make release VERSION=0.1.8)" && exit 1)
	@echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' || (echo "ERROR: VERSION must match X.Y.Z (got '$(VERSION)')" && exit 1)
	@command -v yq >/dev/null 2>&1 || (echo "ERROR: yq required (brew install yq)" && exit 1)
	@test -z "$$(git status --porcelain)" || (echo "ERROR: working tree dirty — commit or stash first" && exit 1)
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "main" || (echo "ERROR: must run on main branch (currently on $$(git rev-parse --abbrev-ref HEAD))" && exit 1)
	@test -z "$$(git tag -l v$(VERSION))" || (echo "ERROR: tag v$(VERSION) already exists locally" && exit 1)
	yq -i -o json '.plugins[0].version = "$(VERSION)"' .claude-plugin/marketplace.json
	yq -i -o json '.version = "$(VERSION)"' assets/plugin/plugin.json
	@git --no-pager diff --stat .claude-plugin/marketplace.json assets/plugin/plugin.json
	git add .claude-plugin/marketplace.json assets/plugin/plugin.json
	git commit -m "chore(release): bump plugin manifests to v$(VERSION)"
	git tag -a v$(VERSION) -m "v$(VERSION)"
	@echo ""
	@echo "Bumped + committed + tagged v$(VERSION) locally. To publish:"
	@echo "  git push origin main && git push origin v$(VERSION)"
