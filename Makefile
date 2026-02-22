PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin

.PHONY: install
install:
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 scripts/ztnet.token.install "$(DESTDIR)$(BINDIR)/ztnet.token.install"
