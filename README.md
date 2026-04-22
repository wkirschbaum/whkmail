# whkmail

A minimal Gmail reader. `whkmaild` runs as a systemd user service, syncing mail to a local SQLite cache. `whkmail` is a terminal UI that talks to the daemon over HTTP.

## Install

```bash
# build both binaries
go build -o ~/.local/bin/whkmaild ./cmd/whkmaild
go build -o ~/.local/bin/whkmail  ./cmd/whkmail

# install systemd user service
# (service file coming — see docs/architecture.md)
```

## First-run setup

1. Create `~/.config/whkmail/config.json`:

```json
{
  "imap_host": "imap.gmail.com",
  "imap_port": 993,
  "email": "you@gmail.com"
}
```

2. Download OAuth2 credentials from [Google Cloud Console](https://console.cloud.google.com/):
   - APIs & Services → Credentials → Create OAuth client ID → Desktop app
   - Download as `~/.config/whkmail/credentials.json`

3. Start the daemon:

```bash
maild
```

On first run it will print a URL and a short code. Open the URL in a browser, enter the code, and authorize. The token is saved — you won't need to do this again.

4. Open the TUI (in a separate terminal):

```bash
whkmail
```

The TUI will auto-start `whkmaild` if it isn't running.

## Usage

| Key | Action |
|-----|--------|
| `j` / `k` | Move down / up |
| `enter` | Open folder or message |
| `esc` / `backspace` | Go back |
| `q` | Quit |

## Cross-compilation

```bash
GOOS=linux GOARCH=arm64 go build -o whkmaild-arm64 ./cmd/whkmaild
GOOS=linux GOARCH=arm64 go build -o whkmail-arm64  ./cmd/whkmail
```

No CGo, no system libraries — the SQLite driver is pure Go.

## Architecture

See [docs/architecture.md](docs/architecture.md).
