package dirs

import (
	"os"
	"path/filepath"
	"strings"
)

func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "whkmail")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "whkmail")
}

func StateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "whkmail")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "whkmail")
}

func ConfigFile() string      { return filepath.Join(ConfigDir(), "config.json") }
func TUIConfigFile() string   { return filepath.Join(ConfigDir(), "tui.json") }
func CredentialsFile() string { return filepath.Join(ConfigDir(), "credentials.json") }
func DBFile() string          { return filepath.Join(StateDir(), "mail.db") }
func LockFile() string        { return filepath.Join(StateDir(), "whkmaild.lock") }
func SocketFile() string      { return filepath.Join(StateDir(), "whkmaild.sock") }
func TokenFile() string       { return filepath.Join(StateDir(), "token.json") }

// safeEmail converts an email address into a filesystem-safe directory name
// by replacing '@' and '.' with '_'.
func safeEmail(email string) string {
	r := strings.NewReplacer("@", "_", ".", "_")
	return r.Replace(email)
}

// AccountStateDir returns the state directory for a specific account.
func AccountStateDir(email string) string {
	return filepath.Join(StateDir(), "accounts", safeEmail(email))
}

// AccountDBFile returns the SQLite database path for an account.
func AccountDBFile(email string) string {
	return filepath.Join(AccountStateDir(email), "mail.db")
}

// AccountTokenFile returns the OAuth2 token path for an account.
func AccountTokenFile(email string) string {
	return filepath.Join(AccountStateDir(email), "token.json")
}

// AccountCredentialsFile returns the OAuth2 credentials path for an account.
// Falls back to the shared credentials file if not account-scoped.
func AccountCredentialsFile(email string) string {
	p := filepath.Join(AccountStateDir(email), "credentials.json")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return CredentialsFile()
}
