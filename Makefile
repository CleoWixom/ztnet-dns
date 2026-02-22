SHELL := /bin/bash

PREFIX ?= /usr
SBINDIR ?= $(PREFIX)/sbin
BINDIR ?= $(PREFIX)/bin
ETCDIR ?= /etc/coredns
UNITDIR ?= /lib/systemd/system
DESTDIR ?=

GO ?= go
GOFLAGS ?=
PKG ?= ./...

COREDNS_VERSION ?= v1.14.0
COREDNS_REPO ?= https://github.com/coredns/coredns.git
COREDNS_WORKDIR ?= /tmp/coredns-ztnet-build
COREDNS_BIN ?= $(COREDNS_WORKDIR)/coredns
PLUGIN_MODULE ?= github.com/CleoWixom/ztnet-dns
PLUGIN_DIR ?= $(CURDIR)

.PHONY: help install-deps ensure-go tidy test verify build-plugin build-coredns \
	install install-helper install-binary install-config install-service clean

help:
	@echo "Targets:"
	@echo "  install-deps   - Install required Linux packages (apt-based systems)"
	@echo "  ensure-go      - Install Go toolchain if missing (apt-based systems)"
	@echo "  tidy           - Run go mod tidy"
	@echo "  test           - Run tests with race detector"
	@echo "  verify         - tidy + tests"
	@echo "  build-plugin   - Compile plugin module packages"
	@echo "  build-coredns  - Build CoreDNS with ztnet plugin in temp workdir"
	@echo "  install        - Full Linux install flow (build + install + service)"
	@echo "  clean          - Remove temporary CoreDNS workdir"

install-deps:
	sudo apt-get update
	sudo apt-get install -y git make build-essential ca-certificates curl dnsutils

ensure-go:
	@if command -v $(GO) >/dev/null 2>&1; then \
		echo "Go already present: $$($(GO) version)"; \
	else \
		echo "Go not found, installing golang via apt..."; \
		sudo apt-get update; \
		sudo apt-get install -y golang; \
		echo "Installed: $$($(GO) version)"; \
	fi

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
	@echo "Built CoreDNS binary: $(COREDNS_BIN)"

install: ensure-go verify build-coredns install-binary install-config install-helper install-service

install-binary:
	install -d "$(DESTDIR)$(SBINDIR)"
	install -m 0755 "$(COREDNS_BIN)" "$(DESTDIR)$(SBINDIR)/coredns"

install-config:
	install -d "$(DESTDIR)$(ETCDIR)"
	@if [ ! -f "$(DESTDIR)$(ETCDIR)/Corefile" ]; then \
		install -m 0640 Corefile.example "$(DESTDIR)$(ETCDIR)/Corefile"; \
		echo "Installed default Corefile to $(DESTDIR)$(ETCDIR)/Corefile"; \
	else \
		echo "Corefile exists, keeping $(DESTDIR)$(ETCDIR)/Corefile"; \
	fi
	install -d -m 0700 "$(DESTDIR)$(ETCDIR)/secrets"
	install -d "$(DESTDIR)$(UNITDIR)"
	install -m 0644 packaging/coredns-ztnet.service "$(DESTDIR)$(UNITDIR)/coredns-ztnet.service"

install-helper:
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 scripts/ztnet.token.install "$(DESTDIR)$(BINDIR)/ztnet.token.install"

install-service:
	@if [ -n "$(DESTDIR)" ]; then \
		echo "DESTDIR set ($(DESTDIR)) -> skipping systemctl/setcap runtime actions"; \
		exit 0; \
	fi
	@if command -v setcap >/dev/null 2>&1; then \
		sudo setcap 'cap_net_bind_service=+ep' "$(SBINDIR)/coredns" || true; \
	fi
	@if command -v systemctl >/dev/null 2>&1; then \
		sudo systemctl daemon-reload; \
		sudo systemctl enable --now coredns-ztnet.service || true; \
	else \
		echo "systemctl not found, skipping service enable/start"; \
	fi

clean:
	rm -rf "$(COREDNS_WORKDIR)"
