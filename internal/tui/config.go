package tui

import (
	"encoding/json"
	"os"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// tuiConfig is the on-disk TUI-only settings file. It lives next to the
// daemon's config.json under the config dir but is owned by the TUI; the
// daemon never reads or writes it. Everything user-facing that is a TUI
// preference belongs here, not in the daemon config.
type tuiConfig struct {
	InputStyle InputStyle `json:"input_style,omitempty"`
}

// LoadInputStyle reads the configured input style from disk, defaulting
// to vim when the file is missing, malformed, or holds an unrecognised
// value. Errors are intentionally swallowed — a broken config shouldn't
// keep the TUI from starting.
func LoadInputStyle() InputStyle {
	b, err := os.ReadFile(dirs.TUIConfigFile())
	if err != nil {
		return StyleVim
	}
	var c tuiConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return StyleVim
	}
	return c.InputStyle.Normalize()
}

// saveInputStyle writes the chosen style back to the TUI config file,
// creating the config dir if it doesn't exist. Unknown fields in the
// existing file are preserved — a newer TUI version that adds settings
// shouldn't lose them when an older binary writes.
func saveInputStyle(style InputStyle) error {
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		return err
	}
	raw := map[string]any{}
	if b, err := os.ReadFile(dirs.TUIConfigFile()); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	raw["input_style"] = string(style)
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dirs.TUIConfigFile(), b, 0o644)
}
