package tui

import (
	"encoding/json"
	"os"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// FolderState controls how a folder is displayed in the TUI.
type FolderState string

const (
	// FolderStateCombined means the folder's messages appear in the Combined
	// tab and the folder also gets its own individual tab.
	FolderStateCombined FolderState = "combined"
	// FolderStateNormal means the folder gets its own tab but is excluded
	// from the Combined tab.
	FolderStateNormal FolderState = "normal"
	// FolderStateHidden removes the folder from the tab bar, the folder
	// list, and all unread counts.
	FolderStateHidden FolderState = "hidden"
)

// folderStateFor returns the configured state for a folder name, defaulting
// to FolderStateNormal. Combined is an explicit opt-in via the folder manager
// so startup never fires unexpected fetches on a fresh install.
func folderStateFor(name string, states map[string]FolderState) FolderState {
	if s, ok := states[name]; ok {
		return s
	}
	return FolderStateNormal
}

// cycleState advances through the combined → normal → hidden → combined
// cycle so a single keypress steps to the next state.
func cycleState(s FolderState) FolderState {
	switch s {
	case FolderStateCombined:
		return FolderStateNormal
	case FolderStateNormal:
		return FolderStateHidden
	default:
		return FolderStateCombined
	}
}

// tuiConfig is the on-disk TUI-only settings file. It lives next to the
// daemon's config.json under the config dir but is owned by the TUI; the
// daemon never reads or writes it. Everything user-facing that is a TUI
// preference belongs here, not in the daemon config.
type tuiConfig struct {
	InputStyle   InputStyle            `json:"input_style,omitempty"`
	FolderStates map[string]FolderState `json:"folder_states,omitempty"`
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

// LoadFolderStates reads per-folder display states from the TUI config file.
// Missing or malformed files return an empty map; missing entries default to
// FolderStateCombined at call sites via folderStateFor.
func LoadFolderStates() map[string]FolderState {
	b, err := os.ReadFile(dirs.TUIConfigFile())
	if err != nil {
		return make(map[string]FolderState)
	}
	var c tuiConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return make(map[string]FolderState)
	}
	if c.FolderStates == nil {
		return make(map[string]FolderState)
	}
	return c.FolderStates
}

// saveFolderState persists a single folder's state into the TUI config file,
// preserving all other existing fields. Writes are atomic at the JSON level —
// a mid-write crash leaves the previous file intact.
func saveFolderState(folder string, state FolderState) error {
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		return err
	}
	raw := map[string]any{}
	if b, err := os.ReadFile(dirs.TUIConfigFile()); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	states, _ := raw["folder_states"].(map[string]any)
	if states == nil {
		states = map[string]any{}
	}
	states[folder] = string(state)
	raw["folder_states"] = states
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dirs.TUIConfigFile(), b, 0o644)
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
