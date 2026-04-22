# Plan

## Done

- [x] Project scaffold — Go module, two binaries (`whkmaild`, `whkmail`), internal package layout
- [x] Systemd-style daemon discovery — random port written to state dir, TUI auto-spawns daemon if not running
- [x] OAuth2 setup — `whkmail auth` loopback-redirect flow with lipgloss UI and step-by-step Google Cloud instructions
- [x] Token persistence — saved to `~/.local/state/whkmail/token.json`, auto-refreshed on expiry
- [x] IMAP sync — connect with XOAUTH2, list folders, fetch 50 most recent messages per folder
- [x] IMAP IDLE — server-push for new mail in INBOX, re-syncs on notification, renews every 20 min
- [x] SQLite cache — pure-Go `modernc.org/sqlite`, WAL mode, upsert-safe schema
- [x] Event bus — fan-out channel broadcast, used for sync → notify and sync → SSE
- [x] HTTP REST API — `/status`, `/folders/{folder}/messages`, `/folders/{folder}/messages/{uid}`, `/events` SSE
- [x] Desktop notifications — D-Bus on Linux, osascript on macOS, build-tag split
- [x] TUI skeleton — bubbletea model, folder list → message list → message view, lipgloss rendering
- [x] Dev scripts — `scripts/auth`, `scripts/daemon`, `scripts/tui`, `scripts/db`

## Up next

- [ ] Message body fetching — IMAP BODY[TEXT] fetch on open, store in SQLite, render in message view
- [ ] Mark as read — send IMAP `\Seen` flag when a message is opened
- [ ] Systemd user service — `.service` file + `scripts/install` to set up and enable it
- [ ] Config validation — clear error on startup if config.json is missing required fields
- [ ] INBOX polling fallback — periodic re-sync for servers that don't support IDLE

## Later

- [ ] Multi-account support
- [ ] Reply / compose
- [ ] Thread view (group messages by subject/references)
- [ ] Search (SQLite FTS5 on subject + body)
- [ ] Attachments — list and save to disk
- [ ] Folder sync control — configurable list of folders to sync (not all)
- [ ] Cross-compilation release builds + install script (like build-watcher)
