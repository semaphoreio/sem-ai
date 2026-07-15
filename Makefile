BINARY    := sem-ai
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
INSTALL   := /usr/local/bin

.PHONY: build install uninstall clean test fmt vet mcpb release tag check-versions

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

# Build a local MCPB (.mcpb) bundle for the current platform into dist/.
# Requires node — uses the official `mcpb` packer (npx fetches it if absent).
#   make mcpb
mcpb: build
	@./scripts/mcpb-pack.sh "$(BINARY)" "$$(go env GOOS)" "$$(go env GOARCH)" dev

# Check plugin manifests are consistent (all four carry the same version).
#   make check-versions            # files match each other
#   make check-versions TAG=0.1.8  # files also match the given tag
check-versions:
	@./scripts/check-manifest-versions.sh $(TAG)

# PR-based release, step 1: bump plugin manifests + commit on a release branch.
#   make release VERSION=0.1.8              # apply
#   make release VERSION=0.1.8 DRY_RUN=1    # preview, no changes
# Open a PR with the commit and squash-merge it, then run `make tag`.
release:
	@./scripts/release.sh $(if $(DRY_RUN),--dry-run,) "$(VERSION)"

# PR-based release, step 2: tag the merged bump on main (run after `make release`
# merges and you `git checkout main && git pull`). Prints the tag-push command
# that triggers the GoReleaser publish pipeline.
#   make tag VERSION=0.1.8
#   make tag VERSION=0.1.8 DRY_RUN=1
tag:
	@./scripts/tag-release.sh $(if $(DRY_RUN),--dry-run,) "$(VERSION)"
