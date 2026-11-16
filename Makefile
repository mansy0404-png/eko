PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
APPDIR ?= $(PREFIX)/share/applications
ICONDIR ?= $(PREFIX)/share/icons/hicolor/scalable/apps
DESTDIR ?=

.PHONY: all ui build install uninstall clean vet

all: ui build

# Build the Svelte UI into internal/api/dist so it can be embedded.
ui:
	@cd web && { [ -d node_modules ] || npm install --no-audit --no-fund; }
	@cd web && npm run build

# Requires internal/api/dist to exist (run `make ui` or `make` once).
build:
	go build -trimpath -ldflags "-s -w" -o eko ./cmd/eko

vet:
	go vet ./...

install: ui build
	install -Dm755 eko $(DESTDIR)$(BINDIR)/eko
	install -Dm755 packaging/eko-ui $(DESTDIR)$(BINDIR)/eko-ui
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/eko.desktop > packaging/eko.desktop.tmp
	install -Dm644 packaging/eko.desktop.tmp $(DESTDIR)$(APPDIR)/eko.desktop
	rm -f packaging/eko.desktop.tmp
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/eko-stop.desktop > packaging/eko-stop.desktop.tmp
	install -Dm644 packaging/eko-stop.desktop.tmp $(DESTDIR)$(APPDIR)/eko-stop.desktop
	rm -f packaging/eko-stop.desktop.tmp
	install -Dm644 packaging/eko.svg $(DESTDIR)$(ICONDIR)/eko.svg
	@echo "installed into $(DESTDIR)$(PREFIX); launch Eko from the app menu or run eko-ui"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/eko $(DESTDIR)$(BINDIR)/eko-ui
	rm -f $(DESTDIR)$(APPDIR)/eko.desktop $(DESTDIR)$(APPDIR)/eko-stop.desktop $(DESTDIR)$(ICONDIR)/eko.svg

clean:
	rm -f eko
	rm -rf internal/api/dist web/node_modules
