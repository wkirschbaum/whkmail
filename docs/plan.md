# Plan

## Done

- [x] Project scaffold — Go module, two binaries (`whkmaild`, `whkmail`), internal package layout
- [x] OAuth2 setup — `whkmail setup` / `whkmail auth` loopback-redirect flow; email auto-detected from Google userinfo API
- [x] Token persistence — `~/.local/state/whkmail/token.json`, auto-refreshed on expiry
- [x] IMAP sync — XOAUTH2 connect, list folders, fetch 200 most recent messages per folder
- [x] IMAP IDLE + polling fallback — server-push for INBOX; polls when IDLE unavailable; 20-min keepalive
- [x] UIDVALIDITY + delta sync — per-folder UIDVALIDITY/UIDNEXT; mismatch wipes + re-syncs; reconnect fetches only new UIDs
- [x] SQLite cache — pure-Go `modernc.org/sqlite`, WAL mode, `SetMaxOpenConns(1)`
- [x] Event bus — fan-out channel broadcast
- [x] HTTP over Unix socket — transport-swappable client
- [x] REST API — `/status`, `/accounts/{a}/folders/{f}/messages`, `/accounts/{a}/folders/{f}/messages/{uid}`, `/accounts/{a}/folders/{f}/messages/{uid}/read`, `/events` SSE
- [x] Multi-account — `Config.Accounts`; per-account SQLite DB and tokens; account-namespaced routes
- [x] Desktop notifications — D-Bus (Linux), osascript (macOS)
- [x] Message body fetching — background worker, cached in SQLite, KindBodyReady event
- [x] HTML → text conversion via `k3a/html2text`
- [x] Systemd user service + TUI auto-spawn
- [x] Daemon lock file (flock)
- [x] Graceful shutdown (WaitGroup drain)

### TUI

- [x] Bubbletea model: accounts → folders → messages → message detail
- [x] Vim bindings: `j`/`k`/`space`/`ctrl+d`/`ctrl+u`/`pgdn`/`pgup`/`g`/`G`
- [x] Viewport scrolling with always-visible cursor highlight (reverse-video)
- [x] `r` refresh with spinner in header; `enter` open; `esc` back
- [x] Mark-as-read after configurable delay (`mark_read_delay_seconds`, default 2s). Generation counter invalidates stale timers on navigation / esc.
- [x] Body cache in `m.messages` — re-opening a read message is instant, no flash
- [x] Prefetch — on folder open: first 2 + first 2 unread; on message open: next 2 after cursor. Dedup via session map; account-scoped keys; errors silenced.
- [x] Body-fetch failure surfacing — worker always publishes `KindBodyReady` (with `Error` field); TUI shows error in body area with retry hint instead of hanging on "Loading…"
- [x] Cursor preserved across refresh; viewport stays aligned after detail-view navigation

### Delete / trash

- [x] `MailProvider.Trash` + `PermanentDelete` — Gmail-faithful: `UID MOVE` to the Trash mailbox (discovered via SPECIAL-USE `\Trash`, fallback to `[Gmail]/Trash` / `Trash` / `Deleted Items`); `\Deleted`+EXPUNGE (prefers `UID EXPUNGE` when UIDPLUS advertised) for the Trash-folder permanent-delete path
- [x] HTTP endpoints `POST /…/trash` and `POST /…/delete`; shared `mutateMessage` plumbing so future per-message ops slot in cheaply
- [x] TUI `d` binding: trashes in normal folders (optimistic local remove); in Trash folder, prompts `Permanently delete this message? (y/N)` then expunges
- [x] `storage.Store.DeleteMessage(folder, uid)` + `Store.UpsertMessages([]Message)` batch — one commit per folder sync instead of 200; body preserved across re-sync

### Reliability under load

- [x] Serialised one-shot IMAP ops (`opMu`) + a long-lived cached `opsConn` so a burst of trashings or body fetches reuses one TCP+TLS+XOAUTH2 handshake instead of one per key-press. Fixed the HTTP 500 when holding `d` to trash many messages at once.
- [x] Stale-connection retry — if an op fails on a cached conn, retry once on a fresh one before surfacing the error.
- [x] Exponential backoff on consecutive ops failures (`opFailures` counter): starts after 2 failures, scales `500ms → 30s` with ±25 % jitter, clears on any success. Survives Gmail rate-limit blips without the TUI turning into a retry storm.
- [x] `Syncer.Close()` + plumbed through `server.RemoveAccount` so account removal releases the cached connection.

### Account management

- [x] `internal/oauth` — extracted from `cmd/whkmaild/config.go` + `cmd/whkmail/auth.go`. Centralises `GmailScope`, credentials parsing, token load/save, userinfo email fetch, and the persisting `TokenFn` closure. Lets the future SMTP sender share the OAuth plumbing without a reach-back through the daemon command package.
- [x] `server.State.RemoveAccount` — cancels the account's syncer goroutine via `WithCancel(…)` option, drops the map entry under a `sync.RWMutex`, closes the store
- [x] `DELETE /accounts/{account}` endpoint
- [x] `whkmail remove <email>` CLI — detaches on the daemon, rewrites `config.json`, removes account-scoped token + DB files, best-effort empty-dir cleanup

### Tests

- [x] Unit tests for pure helpers — `clamp`, `truncate`, `wrapBody`, `padRight`, `messageIndex`, `mergeMessages`, `adjustViewport`, `formatMessageRow`, `isTrashFolder`
- [x] Server handler tests — `HandleStatus`, `HandleMessages`, `HandleMessage`, `HandleMarkRead`, `HandleRemoveAccount`, `RemoveAccount` cancel-and-deregister
- [x] Storage tests — batch `UpsertMessages` insert/update paths, body preservation across re-sync, `DeleteMessage`
- [x] TUI event tests — body failure stores error and suppresses re-fetch; success clears error + re-fetches
- [x] TUI trash UX tests — trash outside Trash, confirm prompt inside Trash, `y` executes, `n` cancels, confirm swallows unrelated keys
- [x] Client↔Server integration — real `httptest` server + `BuildMux` + `Worker`; covers Status/Messages/Message/MarkRead/Trash/PermanentDelete + the end-to-end body-fetch flow + the failure-event path
- [x] Race detector clean across the suite

## Up next

### Reply / compose

The TUI is read-only. Adding a composer turns it into a daily driver.

**Shape of the work:**

1. **`Composer` interface** in `internal/server`:
   ```go
   type Composer interface {
       Send(ctx context.Context, msg types.Draft) error
   }
   ```
   Register additively: `State.AddComposer(email, composer)` so existing `AddAccount` calls are untouched.

2. **`types.Draft`** (wire type) — `To`, `Cc`, `Bcc`, `Subject`, `Body`, `InReplyTo`, `References`.

3. **SMTP client** — new `internal/smtp` package. Gmail: `smtp.gmail.com:465` with XOAUTH2 SASL. Reuses the existing OAuth token source (`GmailIMAPScope = https://mail.google.com/` already grants SMTP).

4. **HTTP endpoint** — `POST /accounts/{a}/send` with JSON `Draft`. Appends to IMAP `[Gmail]/Sent Mail` after successful send so Sent Mail shows up in the list.

5. **TUI compose view** — `viewCompose` state using `bubbles/textinput` for To/Subject and `bubbles/textarea` for Body. `ctrl+s` send, `esc` abort. From `viewMessage`, `R` opens a pre-filled reply (subject with `Re:` prefix, quoted body, `In-Reply-To`/`References` headers). Refresh moves to `ctrl+r` to free `R`.

6. **Drafts (optional for MVP)** — local-only initially. New `drafts` table in the storage adapter and a `DraftStore` interface so it doesn't pollute the read-path `Store`. Add once the straight-through send path works.

**Architecture readiness check:** seams exist; no refactor blocks this work.

- ✅ `MailProvider` pattern is the model — add `Composer` alongside.
- ✅ OAuth scope already covers SMTP.
- ✅ `types` is the wire-type boundary — add `Draft` there.
- ✅ `Storage.Store` is narrow — keep drafts in a separate interface to avoid ballooning it.
- ⚠️ TUI needs `bubbles/textinput` + `bubbles/textarea`. First multi-widget view — adds state but no structural change.
- ⚠️ `service.go` on macOS/Linux doesn't need changes; port doesn't either.

## Later

- [ ] **CONDSTORE flag re-sync + EXPUNGE propagation** — the sync path still only fetches new UIDs, so flag changes (read-elsewhere) and deletions from other clients are invisible locally. Fix with `CHANGEDSINCE` on existing UIDs and a vanished-UID reconciliation pass. High correctness win; blocks a proper multi-device story.
- [ ] **Sync transaction batching won #1** — next perf slice: prepared-statement reuse across folders on a single sync pass, plus a materialised `message_count` / `unread_count` on the folders row so `ListFolders` stops scanning `messages` on every `/status` call.
- [ ] Search — SQLite FTS5 virtual table on `subject + body_text + from_addr`; trigger keeps it synced; new `Store.Search` method + `GET /accounts/{a}/search?q=...` + `/` keybind opens a search bar in the TUI. Architecturally the lightest remaining feature — no new interfaces, no new packages.
- [ ] Thread view — sync `Message-ID` / `In-Reply-To` / `References`; group in list by thread root. Requires storage migration + TUI nested rendering.
- [ ] Attachments — collect `Content-Disposition: attachment` parts during MIME walk; list filenames; save to disk.
- [ ] Pagination — add `offset` (or cursor) to `ListMessages` for folders >200; TUI "load more" at viewport bottom.
- [x] IMAP connection reuse — **done**: the ops connection is cached on the Syncer and reused across `FetchBody` / `MarkRead` / `Trash` / `PermanentDelete`. Per-op latency went from ~200 ms (dial + TLS + XOAUTH2) to ~50 ms (select + command) for anything after the first call.
- [ ] Priority queue for fetches — user-initiated fetches should skip ahead of prefetch jobs so an opened message never waits behind 4 warm-up fetches.
- [ ] Folder sync control — configurable allowlist of folders to sync.
- [ ] Cross-compilation release builds + install script.

## Architecture seams (reference)

```
┌──────────────── daemon (whkmaild) ────────────────┐
│                                                    │
│  Config → loadConfig → []loadedAccount             │
│                          │                         │
│                          ▼                         │
│             imap.Syncer ─┬─ storage.Store (SQLite) │
│                          │                         │
│                  MailProvider (Fetch/MarkRead)     │
│                          │                         │
│                          ▼                         │
│  server.State ──► BuildMux ──► httptest / unix     │
│         │                                          │
│         └─► events.Bus ──► SSE /events             │
│                                                    │
└────────────────────────────────────────────────────┘

                            │  HTTP over unix socket
                            ▼

┌──────────────── TUI (whkmail) ─────────────────────┐
│                                                    │
│  tui.Client (transport-swappable)                  │
│         ▲                                          │
│         │                                          │
│  tui.Model (pure state; tea-driven updates)        │
│    └── render.go (no side effects)                 │
│                                                    │
└────────────────────────────────────────────────────┘
```

**Seams to extend for each planned feature:**

| Feature      | Adds interface / type             | Touches                                      |
| ------------ | --------------------------------- | -------------------------------------------- |
| Compose      | `Composer`, `types.Draft`         | `server.AddComposer`, `internal/smtp`, TUI   |
| Drafts       | `DraftStore`                      | `storage` adapter, new HTTP routes           |
| Search       | `Store.Search(q, limit)`          | storage FTS5, server route, TUI search view  |
| Threads      | fields on `types.Message`         | storage migration, sync MIME header parse    |
| Pagination   | `Store.ListMessages(…, offset)`   | server param, TUI "load more"                |
| IMAP pool    | internal to `internal/imap`       | no public surface change                     |

**Design principles reiterated** (from CLAUDE.md):

- Pure functions down, impure up. TUI `render.go` and helpers like `messageIndex`/`mergeMessages`/`adjustViewport` are pure — tested directly.
- Loose coupling: packages expose interfaces. `AddAccount` / `AddComposer` / `AddSender` lets the daemon mix and match protocol implementations per account.
- Test the pure core. Integration tests use `httptest` — never mock what we own.
