SHELL := /bin/bash

PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=

GO ?= go
GOFLAGS ?=
PKG ?= ./...

COREDNS_VERSION ?= v1.14.0
COREDNS_REPO ?= https://github.com/coredns/coredns.git
COREDNS_WORKDIR ?= /tmp/coredns-ztnet-build
PLUGIN_MODULE ?= github.com/CleoWixom/ztnet-dns
PLUGIN_DIR ?= $(CURDIR)

.PHONY: help install-deps tidy test verify build-plugin build-coredns clean install install-helper

help:
	@echo "Targets:"
	@echo "  install-deps   - Install required Linux build dependencies (apt-based systems)"
	@echo "  tidy           - Run go mod tidy"
	@echo "  test           - Run tests with race detector"
	@echo "  verify         - tidy + tests"
	@echo "  build-plugin   - Compile plugin module packages"
	@echo "  build-coredns  - Build CoreDNS with ztnet plugin in a temp workdir"
	@echo "  install        - Install helper script into \$$(PREFIX)/bin"
	@echo "  clean          - Remove temporary CoreDNS workdir"

install-deps:
	sudo apt-get update
	sudo apt-get install -y git make build-essential ca-certificates curl

tidy:
	$(GO) mod tidy

test:
	$(GO) test $(GOFLAGS) $(PKG) -race -count=1

verify: tidy test

build-plugin:
	$(GO) build $(GOFLAGS) $(PKG)

build-coredns:
	rm -rf "$(COREDNS_WORKDIR)"
	git clone --depth=1 --branch "$(COREDNS_VERSION)" "$(COREDNS_REPO)" "$(COREDNS_WORKDIR)"
	sed -i '/^forward/i ztnet:$(PLUGIN_MODULE)' "$(COREDNS_WORKDIR)/plugin.cfg"
	cd "$(COREDNS_WORKDIR)" && \
		$(GO) mod edit -replace "$(PLUGIN_MODULE)=$(PLUGIN_DIR)" && \
		$(GO) get "$(PLUGIN_MODULE)" && \
		$(GO) generate && \
		$(GO) mod tidy && \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o coredns .
	@echo "Built CoreDNS binary: $(COREDNS_WORKDIR)/coredns"

install: install-helper

install-helper:
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 scripts/ztnet.token.install "$(DESTDIR)$(BINDIR)/ztnet.token.install"

clean:
	rm -rf "$(COREDNS_WORKDIR)"
