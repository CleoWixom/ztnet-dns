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
LDFLAGS ?= -X main.PluginVersion=$(VERSION)

VERSION := $(shell git describe --tags --always --dirty)

COREDNS_VERSION ?= v1.14.0
COREDNS_REPO ?= https://github.com/coredns/coredns.git
COREDNS_WORKDIR ?= /tmp/coredns-ztnet-build
COREDNS_BIN ?= $(COREDNS_WORKDIR)/coredns
PLUGIN_MODULE ?= github.com/CleoWixom/ztnet-dns
PLUGIN_DIR ?= $(CURDIR)

DNS_PORT ?= 53
ZT_INTERFACE_GLOB ?= zt*

.PHONY: help install-deps ensure-go check-port verify-bind-scope tidy test verify build-plugin build-coredns \
	install install-helper install-binary install-config install-service install-zerotier-compat \
	version update uninstall clean

help:
	@echo "Targets:"
	@echo "  install-deps   - Install required Linux packages (apt-based systems)"
	@echo "  ensure-go      - Install Go toolchain if missing (apt-based systems)"
	@echo "  check-port     - Check if DNS port is already occupied"
	@echo "  verify-bind-scope - Show listeners on :53 and zt* interfaces (manual policy check)"
	@echo "  tidy           - Run go mod tidy"
	@echo "  test           - Run tests with race detector"
	@echo "  verify         - tidy + tests"
	@echo "  version        - Print plugin version"
	@echo "  build-plugin   - Compile plugin module packages"
	@echo "  build-coredns  - Build CoreDNS with ztnet plugin in temp workdir"
	@echo "  install        - Full Linux install flow (build + install + service)"
	@echo "  update         - Pull latest repository changes and reinstall"
	@echo "  uninstall      - Stop service and remove CoreDNS ztnet installation"
	@echo "  clean          - Remove temporary CoreDNS workdir"

version:
	@echo $(VERSION)

install-deps:
	sudo apt-get update
	sudo apt-get install -y git make build-essential ca-certificates curl dnsutils net-tools iproute2 ripgrep

ensure-go:
	@if command -v $(GO) >/dev/null 2>&1; then \
		echo "Go already present: $$($(GO) version)"; \
	else \
		echo "Go not found, installing golang via apt..."; \
		sudo apt-get update; \
		sudo apt-get install -y golang; \
		echo "Installed: $$($(GO) version)"; \
	fi


check-port:
	@echo "Checking port $(DNS_PORT) listeners..."
	@if netstat -lntup 2>/dev/null | rg -q "[:.]$(DNS_PORT)\s"; then \
		echo "WARNING: port $(DNS_PORT) is already in use:"; \
		netstat -lntup 2>/dev/null | rg "[:.]$(DNS_PORT)\s" || true; \
		exit 1; \
	else \
		echo "OK: port $(DNS_PORT) is free"; \
	fi

verify-bind-scope:
	@echo "Inspecting active listeners on :$(DNS_PORT)..."
	@netstat -lntup 2>/dev/null | rg "[:.]$(DNS_PORT)\s" || echo "No active listeners on :$(DNS_PORT)"
	@echo "Known zt* interfaces:"
	@if command -v ip >/dev/null 2>&1; then \
		ip -o link show | awk -F': ' '{print $$2}' | rg '^$(ZT_INTERFACE_GLOB)$$' || echo "No zt* interface found"; \
	elif command -v ifconfig >/dev/null 2>&1; then \
		ifconfig -a | awk -F: '/^[a-zA-Z0-9_-]+:/{print $$1}' | rg '^$(ZT_INTERFACE_GLOB)$$' || echo "No zt* interface found"; \
	else \
		echo "Neither ip nor ifconfig found"; \
	fi
	@echo "NOTE: Binding policy (only ZT or all interfaces) is controlled by CoreDNS listen/bind directives in Corefile."

tidy:
	$(GO) mod tidy

test:
	$(GO) test $(GOFLAGS) $(PKG) -race -count=1

verify: tidy test

build-plugin:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" $(PKG)

build-coredns:
	rm -rf "$(COREDNS_WORKDIR)"
	git clone --depth=1 --branch "$(COREDNS_VERSION)" "$(COREDNS_REPO)" "$(COREDNS_WORKDIR)"
	sed -i '/^forward/i ztnet:$(PLUGIN_MODULE)' "$(COREDNS_WORKDIR)/plugin.cfg"
	cd "$(COREDNS_WORKDIR)" && \
		$(GO) mod edit -replace "$(PLUGIN_MODULE)=$(PLUGIN_DIR)" && \
		$(GO) get "$(PLUGIN_MODULE)" && \
		$(GO) generate && \
		$(GO) mod tidy && \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o coredns .
	@echo "Built CoreDNS binary: $(COREDNS_BIN)"

install: ensure-go check-port verify build-coredns install-binary install-config install-helper install-service install-zerotier-compat

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
	install -m 0755 packaging/ztnetool "$(DESTDIR)$(BINDIR)/ztnetool"

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

install-zerotier-compat:
	@if [ -n "$(DESTDIR)" ]; then \
		echo "DESTDIR set ($(DESTDIR)) -> skipping zerotier-one.service patch"; \
		exit 0; \
	fi
	@if [ -f "/lib/systemd/system/zerotier-one.service" ]; then \
		echo "Patching /lib/systemd/system/zerotier-one.service to add -U"; \
		sudo sed -i 's#\(/usr/sbin/zerotier-one\)\([[:space:]]\|$$\)#\1 -U\2#g' /lib/systemd/system/zerotier-one.service; \
		sudo systemctl daemon-reload || true; \
	else \
		echo "zerotier-one.service not found, skipping"; \
	fi

update:
	git pull --ff-only
	$(MAKE) install

uninstall:
	@if command -v systemctl >/dev/null 2>&1; then \
		sudo systemctl disable --now coredns-ztnet.service || true; \
	fi
	sudo rm -f "$(SBINDIR)/coredns"
	sudo rm -f "$(UNITDIR)/coredns-ztnet.service"
	sudo rm -rf "$(ETCDIR)"
	@if command -v systemctl >/dev/null 2>&1; then \
		sudo systemctl daemon-reload || true; \
	fi
clean:
	rm -rf "$(COREDNS_WORKDIR)"
