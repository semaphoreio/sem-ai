BINARY    := sem-ai
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
INSTALL   := /usr/local/bin

.PHONY: build install uninstall clean test fmt vet

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
