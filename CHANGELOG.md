# Changelog

## Unreleased

### Added
- Initial project scaffold
- `whkmaild` daemon: IMAP sync with XOAUTH2, IDLE loop, SQLite cache, HTTP REST API, SSE event stream
- `whkmail` TUI: folder list, message list, message view via bubbletea + lipgloss
- Desktop notifications via D-Bus (Linux) and osascript (macOS)
- OAuth2 device flow for headless first-run authentication
- Daemon auto-discovery: TUI reads port file, spawns daemon if not running
- Pure-Go SQLite via `modernc.org/sqlite` — no CGo, trivial cross-compilation
