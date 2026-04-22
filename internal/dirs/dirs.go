package dirs

import (
	"os"
	"path/filepath"
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

func ConfigFile() string  { return filepath.Join(ConfigDir(), "config.json") }
func DBFile() string      { return filepath.Join(StateDir(), "mail.db") }
func SocketFile() string  { return filepath.Join(StateDir(), "whkmaild.sock") }
func TokenFile() string   { return filepath.Join(StateDir(), "token.json") }
