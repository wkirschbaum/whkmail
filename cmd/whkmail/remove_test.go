package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// readConfig reads config.json out of the tempdir established by
// t.Setenv("XDG_CONFIG_HOME", …) — keeps the tests below compact.
func readConfig(t *testing.T) types.Config {
	t.Helper()
	raw, err := os.ReadFile(dirs.ConfigFile())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg
}

func writeConfigFile(t *testing.T, cfg types.Config) {
	t.Helper()
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(dirs.ConfigFile(), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRemoveAccountFromConfig_MultiAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeConfigFile(t, types.Config{
		Accounts: []types.AccountConfig{
			{Email: "a@ex", IMAPHost: "imap.ex", IMAPPort: 993},
			{Email: "b@ex", IMAPHost: "imap.ex", IMAPPort: 993},
		},
	})

	if err := removeAccountFromConfig("a@ex"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	cfg := readConfig(t)
	if len(cfg.Accounts) != 1 {
		t.Fatalf("expected 1 account left, got %d", len(cfg.Accounts))
	}
	if cfg.Accounts[0].Email != "b@ex" {
		t.Errorf("wrong account kept: %s", cfg.Accounts[0].Email)
	}
}

func TestRemoveAccountFromConfig_LegacyTopLevel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeConfigFile(t, types.Config{
		Email: "solo@ex", IMAPHost: "imap.ex", IMAPPort: 993,
	})

	if err := removeAccountFromConfig("solo@ex"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	cfg := readConfig(t)
	if cfg.Email != "" || cfg.IMAPHost != "" || cfg.IMAPPort != 0 {
		t.Errorf("legacy fields not cleared: %+v", cfg)
	}
}

func TestRemoveAccountFromConfig_UnknownAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeConfigFile(t, types.Config{
		Accounts: []types.AccountConfig{{Email: "a@ex"}},
	})

	err := removeAccountFromConfig("ghost@ex")
	if err == nil {
		t.Fatal("expected error for unknown account")
	}

	// Config must stay untouched so the user can retry.
	cfg := readConfig(t)
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Email != "a@ex" {
		t.Errorf("config mutated on failure: %+v", cfg.Accounts)
	}
}

func TestRemoveAccountFromConfig_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// No config.json written — should be treated as nothing-to-do, not an error.
	if err := removeAccountFromConfig("anyone@ex"); err != nil {
		t.Errorf("missing config should not error, got %v", err)
	}
}

func TestRemoveAccountFromConfig_MalformedFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(dirs.ConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write garbage.
	if err := os.WriteFile(dirs.ConfigFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := removeAccountFromConfig("anyone@ex")
	if err == nil {
		t.Error("expected parse error")
	}
}

func TestRemoveAccountFromConfig_LastAccount_LeavesEmptyArray(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeConfigFile(t, types.Config{
		Accounts: []types.AccountConfig{{Email: "only@ex"}},
	})

	if err := removeAccountFromConfig("only@ex"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	raw, err := os.ReadFile(dirs.ConfigFile())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Sanity: file is still a valid empty-accounts config, so `whkmail auth`
	// can re-populate without the user having to edit JSON by hand.
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Errorf("config no longer parses: %v\n%s", err, raw)
	}
	if len(cfg.Accounts) != 0 {
		t.Errorf("expected empty accounts, got %+v", cfg.Accounts)
	}
	// Ensure the file actually lives where we expect.
	if _, err := os.Stat(filepath.Join(dirs.ConfigDir(), "config.json")); err != nil {
		t.Errorf("config.json missing: %v", err)
	}
}
