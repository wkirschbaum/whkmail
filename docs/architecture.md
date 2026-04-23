# Architecture

`whkmail` is a Go application split into two binaries: `whkmaild` (daemon) and `whkmail` (TUI). They communicate over a Unix socket with JSON + Server-Sent Events for real-time push.

## System overview

```
Gmail (IMAP over TLS)
        │
        │ go-imap/v2 + XOAUTH2
        ▼
┌──────────────────────────────────────────────┐
│                 whkmaild daemon              │
│                                              │
│  internal/oauth/   Google OAuth + token I/O  │
│  internal/imap/    Syncer — IMAP + IDLE +    │
│                    MIME + Trash + \Seen      │
│         │ emits                              │
│         ▼                                    │
│  internal/events/  fan-out Bus               │
│         │                                    │
│  ┌──────┴───────┬───────────────┐            │
│  ▼              ▼               ▼            │
│  storage/       notify/         server/      │
│  Store +        linux (D-Bus) + HTTP +       │
│  SQLite         macos (osascr.) SSE          │
│                                              │
└──────────────────────────────────────────────┘
        │ HTTP REST + SSE over Unix socket
        ▼
┌──────────────────────────────────────────────┐
│                 whkmail TUI                  │
│                                              │
│  internal/tui/   bubbletea Model + lipgloss  │
│                  render + swappable Client   │
└──────────────────────────────────────────────┘
```

## Data flow

```
Gmail IMAP IDLE
      │ new message notification
      ▼
syncMailbox() — fetch headers → store.UpsertMessages() (single tx)
      │
      └── Publish(KindNewMessage) → events.Bus
              │
        ┌─────┴───────┐
        ▼             ▼
  notify.Run()   SSE /events → TUI
  (desktop)
```

```
TUI opens a message
      │
      ▼  GET /accounts/{a}/folders/{f}/messages/{uid}
  handlers.go seeds the body-fetch queue if uncached → 200 OK (empty body)
      │
      ▼  (background)
  server.Worker()  →  imap.Syncer.FetchBody  →  store.SetBodyText
      │
      └── Publish(KindBodyReady {error?}) → events.Bus
              │
              ▼ SSE
            TUI re-fetches → body rendered (or error banner)
```

## Daemon discovery

1. `whkmaild` binds a Unix socket at `$XDG_STATE_HOME/whkmail/whkmaild.sock`.
2. An advisory `flock(2)` on `whkmaild.lock` guarantees a single daemon instance per user.
3. `whkmail` TUI checks the socket; if dead, it starts the daemon via `systemctl --user start whkmaild.service` (Linux), `launchctl start com.whkmail.daemon` (macOS), or direct spawn as a last resort.
4. On shutdown, the socket is removed.

## Package layout

```
cmd/
  whkmaild/  daemon entry: loads config, opens stores, starts Syncer per account
  whkmail/   TUI entry + subcommands: setup / auth / remove

internal/
  dirs/      XDG-aware paths for config, state, socket, DB, token
  types/     shared wire types: Message, Folder, Config, API responses
  events/    fan-out EventBus (channel-based, non-blocking publish, error-bearing)
  oauth/     Google OAuth2: credentials, token load/save, token refresh, userinfo
  storage/   Store interface + SQLite adapter (modernc.org/sqlite; CGo-free)
  imap/      Syncer (implements server.MailProvider) + XOAUTH2 SASL
             ├── imap.go   — Syncer, connect
             ├── sync.go   — Run, IDLE, poll, delta sync, MIME→Message
             ├── body.go   — FetchBody, MarkRead, MIME text extraction
             ├── trash.go  — Trash (UID MOVE), PermanentDelete, SPECIAL-USE
             └── oauth.go  — XOAUTH2 SASL mechanism
  server/    In-memory account registry + HTTP surface
             ├── state.go    — State, accountState, Add/Remove, snapshot
             ├── handlers.go — every HTTP handler + BuildMux
             └── server.go   — Serve, Worker (body-fetch), lifecycle
  notify/    desktop notifications (build-tag-split: linux / macos)
  tui/       bubbletea Model + lipgloss rendering + HTTP+SSE client
             ├── model.go     — state, message types, NewModel
             ├── update.go    — Update, event dispatch
             ├── keys.go      — keybind map, moveCursor, scroll
             ├── actions.go   — openMessage, trash, permanentDelete, merge
             ├── prefetch.go  — dedup-aware body prefetch helpers
             ├── cmds.go      — tea.Cmd factories (fetchStatus, …, trashCmd)
             ├── viewport.go  — pure helpers: adjustViewport, clamp, mergeMessages
             ├── render.go    — lipgloss views
             └── client.go    — swappable transport HTTP + SSE client
```

## REST API

| Method  | Endpoint                                                            | Description                                       |
|---------|---------------------------------------------------------------------|---------------------------------------------------|
| GET     | `/status`                                                           | All accounts, folders, sync state                 |
| GET     | `/accounts/{account}/folders/{folder}/messages`                     | Headers for the most recent 200 messages          |
| GET     | `/accounts/{account}/folders/{folder}/messages/{uid}`               | Single message; enqueues body fetch if uncached   |
| POST    | `/accounts/{account}/folders/{folder}/messages/{uid}/read`          | Flag \Seen                                        |
| POST    | `/accounts/{account}/folders/{folder}/messages/{uid}/trash`         | UID MOVE to account's Trash mailbox               |
| POST    | `/accounts/{account}/folders/{folder}/messages/{uid}/delete`        | \Deleted + EXPUNGE (use from Trash)               |
| DELETE  | `/accounts/{account}`                                               | Deregister account from running daemon            |
| GET     | `/events`                                                           | SSE stream of `events.Event` JSON lines           |

## Interface seams

| Interface                   | Package     | Implementers                  |
|-----------------------------|-------------|-------------------------------|
| `storage.Store`             | storage     | `*storage.SQLite`             |
| `server.MailStore`          | server      | `*storage.SQLite` (structural)|
| `server.MailProvider`       | server      | `*imap.Syncer`                |
| `notify.Notifier`           | notify      | Linux (D-Bus), macOS (osascript) |
| (TUI transport)             | tui         | `newClient(base, RoundTripper)` — Unix socket in production, `httptest.Server` in tests |

## Key design decisions

See [decisions.md](decisions.md).
