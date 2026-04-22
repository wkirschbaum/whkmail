# Architecture

`whkmail` is a Go application split into two binaries: `whkmaild` (daemon) and `whkmail` (TUI). They communicate via a local HTTP REST API with SSE for real-time push.

## System overview

```
Gmail (IMAP over TLS)
        │
        │ go-imap/v2 + XOAUTH2
        ▼
┌──────────────────────────────────────────┐
│               maild daemon               │
│                                          │
│  internal/sync/                          │
│  ├── imap.go   — IMAP sync + IDLE loop   │
│  └── oauth.go  — XOAUTH2 SASL mechanism  │
│         │ emits                          │
│         ▼                                │
│  internal/events/                        │
│  └── events.go — fan-out event bus       │
│         │                                │
│  ┌──────┴──────────┐                     │
│  ▼                 ▼                     │
│  internal/store/   internal/notify/      │
│  └── sqlite.go     ├── notify.go (iface) │
│                    ├── linux.go (D-Bus)  │
│                    └── macos.go (osascr) │
│                                          │
│  internal/server/                        │
│  └── server.go — HTTP + SSE endpoints   │
└──────────────────────────────────────────┘
        │ HTTP REST + SSE
        ▼
┌──────────────────────────────────────────┐
│                mail TUI                  │
│                                          │
│  internal/tui/                           │
│  ├── app.go    — bubbletea model         │
│  ├── render.go — lipgloss views          │
│  └── client.go — HTTP + SSE client      │
└──────────────────────────────────────────┘
```

## Data flow

```
Gmail IMAP IDLE
      │ new message notification
      ▼
syncMailbox() — fetch headers → UpsertMessage() → SQLite
      │
      └── Publish(KindNewMessage) → EventBus
              │
        ┌─────┴──────┐
        ▼             ▼
  notify.Run()    SSE /events
  (D-Bus notif)   (→ TUI client)
```

## Daemon discovery

Identical pattern to build-watcher:

1. `whkmaild` binds to a random port on `127.0.0.1` at startup.
2. Writes the port number to `~/.local/state/whkmail/port`.
3. `whkmail` TUI reads the port file and verifies TCP connectivity.
4. If the port file is missing or unreachable, `whkmail` spawns `whkmaild` and polls until the port file appears (up to 10s).
5. On shutdown, `whkmaild` removes the port file.

## Package layout

```
cmd/
  whkmaild/        daemon entry point + config/auth loading
  whkmail/         TUI entry point + daemon discovery

internal/
  dirs/           XDG-aware paths for config, state, DB, port, token
  types/          shared wire types: Message, Folder, Config, HTTP responses
  events/         fan-out EventBus (channel-based, non-blocking publish)
  store/          SQLite via modernc.org/sqlite (pure Go, CGo-free)
  sync/           IMAP sync, IDLE loop, XOAUTH2 SASL
  server/         HTTP server, REST handlers, SSE stream
  notify/         desktop notifications (build-tag-split: linux/macos)
  tui/            bubbletea Model, lipgloss rendering, HTTP client
```

## REST API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Folder list, sync state, last sync time |
| `/folders/{folder}/messages` | GET | List 50 most recent messages in folder |
| `/folders/{folder}/messages/{uid}` | GET | Single message with body |
| `/events` | GET | SSE stream of EventBus events |

## Key design decisions

See [decisions.md](decisions.md).
