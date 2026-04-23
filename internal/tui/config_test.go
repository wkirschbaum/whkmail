package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// withTempConfigDir points XDG_CONFIG_HOME at a fresh tempdir so load/save
// round-trip tests can run without touching the user's real config.
func withTempConfigDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	return tmp
}

func TestLoadInputStyle_MissingFile_DefaultsVim(t *testing.T) {
	withTempConfigDir(t)
	if got := LoadInputStyle(); got != StyleVim {
		t.Errorf("missing file: got %q, want %q", got, StyleVim)
	}
}

func TestLoadInputStyle_MalformedJSON_DefaultsVim(t *testing.T) {
	withTempConfigDir(t)
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dirs.TUIConfigFile(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := LoadInputStyle(); got != StyleVim {
		t.Errorf("malformed file: got %q, want %q", got, StyleVim)
	}
}

func TestLoadInputStyle_UnknownValue_DefaultsVim(t *testing.T) {
	withTempConfigDir(t)
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dirs.TUIConfigFile(), []byte(`{"input_style":"dvorak"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := LoadInputStyle(); got != StyleVim {
		t.Errorf("unknown value: got %q, want %q", got, StyleVim)
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	withTempConfigDir(t)
	for _, want := range []InputStyle{StyleVim, StyleEmacs} {
		if err := saveInputStyle(want); err != nil {
			t.Fatalf("saveInputStyle(%q): %v", want, err)
		}
		if got := LoadInputStyle(); got != want {
			t.Errorf("roundtrip %q: got %q", want, got)
		}
	}
}

func TestSaveInputStyle_CreatesConfigDir(t *testing.T) {
	base := withTempConfigDir(t)
	// Sanity: the config dir doesn't exist yet.
	if _, err := os.Stat(filepath.Join(base, "whkmail")); !os.IsNotExist(err) {
		t.Fatalf("expected config dir missing, stat err = %v", err)
	}
	if err := saveInputStyle(StyleEmacs); err != nil {
		t.Fatalf("saveInputStyle: %v", err)
	}
	if _, err := os.Stat(dirs.TUIConfigFile()); err != nil {
		t.Errorf("expected file created, stat err = %v", err)
	}
}

func TestSaveInputStyle_PreservesUnknownFields(t *testing.T) {
	withTempConfigDir(t)
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A future release could add fields to tui.json. Writing back from an
	// older TUI must not wipe them.
	original := `{"input_style":"vim","theme":"dark"}`
	if err := os.WriteFile(dirs.TUIConfigFile(), []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := saveInputStyle(StyleEmacs); err != nil {
		t.Fatalf("saveInputStyle: %v", err)
	}

	b, err := os.ReadFile(dirs.TUIConfigFile())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["input_style"] != "emacs" {
		t.Errorf("input_style = %v, want emacs", raw["input_style"])
	}
	if raw["theme"] != "dark" {
		t.Errorf("unknown field dropped: theme = %v, want dark", raw["theme"])
	}
}
