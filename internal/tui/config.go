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
	InputStyle   InputStyle             `json:"input_style,omitempty"`
	FolderStates map[string]FolderState `json:"folder_states,omitempty"`
}

// readTUIConfig is the single file-read path shared by all Load* callers.
// Errors are intentionally swallowed — a missing or malformed file returns
// a zero-value config so the TUI starts with sane defaults.
func readTUIConfig() tuiConfig {
	b, err := os.ReadFile(dirs.TUIConfigFile())
	if err != nil {
		return tuiConfig{}
	}
	var c tuiConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return tuiConfig{}
	}
	if c.FolderStates == nil {
		c.FolderStates = make(map[string]FolderState)
	}
	return c
}

// LoadInputStyle reads the configured input style from disk, defaulting
// to vim when the file is missing, malformed, or holds an unrecognised value.
func LoadInputStyle() InputStyle {
	return readTUIConfig().InputStyle.Normalize()
}

// LoadFolderStates reads per-folder display states from the TUI config file.
// Missing or malformed files return an empty map.
func LoadFolderStates() map[string]FolderState {
	return readTUIConfig().FolderStates
}

// updateTUIConfig is the single write path for all save* callers. It reads
// the existing file as a raw map (preserving unknown fields from future TUI
// versions), applies mutate, then writes back atomically at the JSON level.
func updateTUIConfig(mutate func(raw map[string]any)) error {
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		return err
	}
	raw := map[string]any{}
	if b, err := os.ReadFile(dirs.TUIConfigFile()); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	mutate(raw)
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dirs.TUIConfigFile(), b, 0o644)
}

// saveFolderState persists a single folder's state into the TUI config file,
// preserving all other existing fields.
func saveFolderState(folder string, state FolderState) error {
	return updateTUIConfig(func(raw map[string]any) {
		states, _ := raw["folder_states"].(map[string]any)
		if states == nil {
			states = map[string]any{}
		}
		states[folder] = string(state)
		raw["folder_states"] = states
	})
}

// saveInputStyle writes the chosen style back to the TUI config file,
// creating the config dir if it doesn't exist.
func saveInputStyle(style InputStyle) error {
	return updateTUIConfig(func(raw map[string]any) {
		raw["input_style"] = string(style)
	})
}
