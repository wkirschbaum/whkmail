# Architectural Decisions

## Go over Rust

**Decision:** Go.

Go's cross-compilation is a one-liner (`GOOS`/`GOARCH` env vars, no toolchain setup). The Charm ecosystem (`bubbletea`, `lipgloss`) is the best TUI stack available in any language right now. Goroutines are a natural fit for the IMAP IDLE loop + background sync pattern. Rust would give smaller binaries and a stronger type system, but neither advantage pays off for a mail reader at this scale.

## Two binaries, one module

**Decision:** `whkmaild` (daemon) + `whkmail` (TUI), built from a single Go module.

Mirrors build-watcher's architecture. The daemon runs as a systemd user service and owns all network I/O, OAuth tokens, and the SQLite cache. The TUI is stateless — it queries the daemon over HTTP and renders what it gets back. This means the TUI can crash and restart without losing sync state, and multiple TUI sessions can connect to the same daemon simultaneously.

## HTTP + SSE over Unix socket or gRPC

**Decision:** Plain HTTP on a random loopback port.

The port is written to `~/.local/state/whkmail/port` on startup and removed on shutdown. Simpler than a Unix socket (no permissions, works on macOS too), and SSE is sufficient for one-directional push from daemon to TUI. No proto files, no code generation, no external toolchain dependency.

## IMAP over Gmail REST API

**Decision:** IMAP with XOAUTH2.

IMAP is not Gmail-specific — the same code works against any provider. The Gmail REST API offers better threading and label support but requires a Gmail-specific client and ties the app to one provider. IMAP IDLE gives server-push for new mail without polling.

## modernc.org/sqlite over mattn/go-sqlite3

**Decision:** `modernc.org/sqlite` (pure Go, CGo-free).

`mattn/go-sqlite3` requires CGo, which breaks cross-compilation unless you set up a cross-compiler toolchain. `modernc.org/sqlite` is a machine-translated version of the same SQLite C source — identical behaviour, pure Go, cross-compiles with a single env var.

## D-Bus via godbus over esiqveland/notify

**Decision:** `godbus/dbus/v5` directly, with build-tag-split linux/macos files.

`esiqveland/notify` is a thin wrapper but adds an import for something we can call directly in ~30 lines. The build tag split (`//go:build linux` / `//go:build darwin`) mirrors build-watcher's `platform/linux` and `platform/macos` directories and keeps the fallback path explicit.

## OAuth2 device flow for first-run auth

**Decision:** Device authorization flow (`oauth2.Config.DeviceAuth`).

The daemon is headless (systemd service), so a redirect-based flow requiring a browser callback server is awkward. The device flow prints a URL and code to stderr, the user opens it once, and the token is persisted to `~/.local/state/whkmail/token.json` for all future runs. The `TokenSource` wrapper auto-refreshes the access token transparently.
