BINARY    := sem-ai
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
INSTALL   := /usr/local/bin

.PHONY: build install uninstall clean test fmt vet release check-versions

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

# Check plugin manifests are consistent.
#   make check-versions            # both files match each other
#   make check-versions TAG=0.1.8  # both also match the given tag
check-versions:
	@./scripts/check-manifest-versions.sh $(TAG)

# Bump plugin manifests, commit, and tag a new release.
#   make release VERSION=0.1.8              # apply
#   make release VERSION=0.1.8 DRY_RUN=1    # preview, no changes
# Thin wrapper around scripts/release.sh. Does NOT push — review
# locally, then run the printed git push commands.
release:
	@./scripts/release.sh $(if $(DRY_RUN),--dry-run,) "$(VERSION)"
