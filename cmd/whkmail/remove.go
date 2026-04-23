package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/tui"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// runRemove detaches an account from whkmail. It tells a running daemon to
// stop syncing the account, rewrites config.json without it, and deletes the
// account-scoped token + database. The daemon continues to serve the
// remaining accounts; no restart required.
//
// On-disk cleanup is best-effort: we log and proceed on each step so a
// half-broken install (e.g. DB already missing) can still be fully removed.
func runRemove(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: whkmail remove <email>")
	}
	email := strings.TrimSpace(args[0])
	if email == "" {
		return fmt.Errorf("email is required")
	}

	if err := confirmRemove(email); err != nil {
		return err
	}

	// 1. Tell the daemon (if one is running) to stop syncing this account.
	//    If the daemon isn't running, that's fine — nothing in-memory to clean.
	client := tui.NewClient()
	if err := client.RemoveAccount(ctx, email); err != nil {
		fmt.Fprintf(os.Stderr, "  %s daemon detach failed: %v\n", styleMuted.Render("·"), err)
		fmt.Fprintf(os.Stderr, "  %s continuing with on-disk cleanup\n", styleMuted.Render("·"))
	} else {
		fmt.Printf("  %s daemon: detached\n", styleOK.Render("✓"))
	}

	// 2. Rewrite config.json without the account. Only an error if the file
	//    exists and is malformed — a missing config is fine (nothing to edit).
	if err := removeAccountFromConfig(email); err != nil {
		fmt.Fprintf(os.Stderr, "  %s config: %v\n", styleErr.Render("!"), err)
	} else {
		fmt.Printf("  %s config: entry removed\n", styleOK.Render("✓"))
	}

	// 3. Delete account-scoped state files (token + sqlite db).
	removed := 0
	for _, path := range []string{
		dirs.AccountTokenFile(email),
		dirs.AccountDBFile(email),
	} {
		if err := os.Remove(path); err == nil {
			removed++
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  %s remove %s: %v\n", styleErr.Render("!"), path, err)
		}
	}
	if removed > 0 {
		fmt.Printf("  %s removed %d file(s) under %s\n",
			styleOK.Render("✓"), removed, stylePath.Render(dirs.AccountStateDir(email)))
	}
	// Try to remove the now-empty account dir (best-effort; fails silently if
	// non-empty — e.g. if the user keeps a credentials.json override).
	_ = os.Remove(dirs.AccountStateDir(email))

	fmt.Println()
	fmt.Printf("  %s %s removed.\n", styleOK.Render("✓"), email)
	return nil
}

// confirmRemove prompts the user; any response other than "yes" aborts.
// Runs only on a TTY — pipes skip the prompt to keep scripting easy.
func confirmRemove(email string) error {
	if !isTTY() {
		return nil
	}
	fmt.Printf("\n  This will delete %s and all cached mail for it. Continue? (yes/N) ",
		styleStep.Render(email))
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ToLower(line)) != "yes" {
		return fmt.Errorf("aborted")
	}
	return nil
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// removeAccountFromConfig reads config.json, filters out the matching account,
// and writes the file back. Missing config file is treated as success. If the
// removed account was the last one, the Accounts array is left empty so a
// re-run of `whkmail auth` can re-populate the file.
func removeAccountFromConfig(email string) error {
	path := dirs.ConfigFile()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	// Handle both legacy (top-level email) and multi-account forms.
	changed := false
	if cfg.Email == email {
		cfg.Email = ""
		cfg.IMAPHost = ""
		cfg.IMAPPort = 0
		changed = true
	}
	if len(cfg.Accounts) > 0 {
		kept := cfg.Accounts[:0]
		for _, a := range cfg.Accounts {
			if a.Email == email {
				changed = true
				continue
			}
			kept = append(kept, a)
		}
		cfg.Accounts = kept
	}
	if !changed {
		return fmt.Errorf("account %q not found in config", email)
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}
