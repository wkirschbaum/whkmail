# Plan

## Done

- [x] Project scaffold — Go module, two binaries (`whkmaild`, `whkmail`), internal package layout
- [x] OAuth2 setup — `whkmail setup` / `whkmail auth` loopback-redirect flow; email auto-detected from Google userinfo API; lipgloss wizard UI with step-by-step Google Cloud instructions
- [x] Token persistence — saved to `~/.local/state/whkmail/token.json`, auto-refreshed on expiry
- [x] Config auto-write — `config.json` written from OAuth userinfo response, no manual email entry
- [x] IMAP sync — connect with XOAUTH2, list folders, fetch 50 most recent messages per folder
- [x] IMAP IDLE — server-push for new mail in INBOX, re-syncs on notification, renews every 20 min
- [x] SQLite cache — pure-Go `modernc.org/sqlite`, WAL mode, `SetMaxOpenConns(1)`, upsert-safe schema
- [x] Event bus — fan-out channel broadcast, sync → notify and sync → SSE
- [x] HTTP over Unix socket — IPC via `~/.local/state/whkmail/whkmaild.sock`; transport-swappable client
- [x] REST API — `/status`, `/folders/{folder}/messages`, `/folders/{folder}/messages/{uid}`, `/events` SSE
- [x] Desktop notifications — D-Bus on Linux, osascript on macOS, build-tag split
- [x] TUI skeleton — bubbletea model, folder list → message list → message view, lipgloss rendering
- [x] Systemd user service — installed and restarted by `whkmail setup`; TUI auto-starts via `systemctl start` on Linux, falls back to direct spawn
- [x] `whkmail setup` wizard — idempotent: skips OAuth if already authorized, always reinstalls service
- [x] Daemon discovery — socket liveness check; prefers systemd/launchd over direct spawn
- [x] Unit + integration tests — 26 tests across events, store, server handlers, sync parsing, TUI rendering
- [x] Loose coupling — `MailStore` interface in server package; injectable transport in TUI client
- [x] Dev scripts — `scripts/setup`, `scripts/restart`, `scripts/daemon`, `scripts/tui`, `scripts/auth`, `scripts/db`

## Up next

- [ ] Message body fetching — IMAP `BODY[TEXT]` fetch on open, store in SQLite, render in message view
- [ ] Mark as read — send IMAP `\Seen` flag when a message is opened
- [ ] TUI: j/k message navigation within message view (next/prev without returning to list)
- [ ] TUI: R to refresh current view with visual feedback
- [ ] INBOX polling fallback — periodic re-sync for servers that don't support IDLE

## Later

- [ ] Multi-account support
- [ ] Reply / compose
- [ ] Thread view (group by subject/references)
- [ ] Search (SQLite FTS5 on subject + body)
- [ ] Attachments — list and save to disk
- [ ] Folder sync control — configurable list of folders to sync
- [ ] Cross-compilation release builds + GitHub release install script (like build-watcher)
- [ ] macOS launchd service support in `whkmail setup`
