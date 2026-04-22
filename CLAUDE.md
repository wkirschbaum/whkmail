# CLAUDE.md

## Project

`whkmail` — a minimal Gmail reader with a systemd daemon (`whkmaild`) and a bubbletea TUI (`whkmail`).

## Repository layout

```
cmd/whkmaild/        daemon: IMAP sync, HTTP server, OAuth2 auth
cmd/whkmail/         TUI: bubbletea app, daemon discovery
internal/dirs/    XDG path helpers
internal/types/   shared wire types
internal/events/  fan-out EventBus
internal/store/   SQLite cache (pure Go, no CGo)
internal/sync/    IMAP sync + IDLE loop + XOAUTH2
internal/server/  HTTP REST + SSE
internal/notify/  desktop notifications (linux/macos build tags)
internal/tui/     bubbletea Model, lipgloss render, HTTP client
docs/             architecture and decision docs
```

## Commands

```bash
go build ./...                            # build everything
go build -o bin/whkmaild ./cmd/whkmaild         # daemon binary
go build -o bin/whkmail  ./cmd/whkmail          # TUI binary

# cross-compile (no extra toolchain needed — pure Go)
GOOS=linux GOARCH=arm64 go build -o bin/whkmaild-arm64 ./cmd/whkmaild
```

## Config

On first run, `whkmaild` looks for:
- `~/.config/whkmail/config.json` — IMAP host/port/email
- `~/.config/whkmail/credentials.json` — Google OAuth2 client credentials (from Google Cloud Console)

If `~/.local/state/whkmail/token.json` is missing, it runs the OAuth2 device flow and writes the token there.

## Architecture

See [docs/architecture.md](docs/architecture.md) and [docs/decisions.md](docs/decisions.md).

## Design principles

- **Pure functions down, impure up.** Computation and data transformation live in
  pure functions (no I/O, no global state, no side effects) at the bottom of the
  call stack. I/O — database, network, filesystem — is confined to the top.
  Handlers and sync loops orchestrate; helpers just transform.
- **Loose coupling, high cohesion.** Packages expose narrow interfaces, not
  concrete types. Callers depend on behaviour (interfaces), not implementation.
  Each package owns exactly one concept.
- **Test the pure core.** Unit tests target pure functions directly — no mocks
  needed. Integration tests use in-memory SQLite or `httptest`; never mock what
  you own.

## Rules

- Systemd/launchd service management is done only by `whkmail setup` (`cmd/whkmail/service.go`). No other code should touch the service.
- **NEVER** use `mattn/go-sqlite3` (requires CGo, breaks cross-compilation) — use `modernc.org/sqlite`.
- **NEVER** use CGo in any package — the whole point is a CGo-free single static binary.
- The TUI is stateless. All mail state lives in the daemon's SQLite DB.
- The daemon owns the OAuth2 token and IMAP connection. The TUI never talks to Gmail directly.
