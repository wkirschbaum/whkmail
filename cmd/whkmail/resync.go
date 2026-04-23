package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/types"
	_ "modernc.org/sqlite"
)

// runResync wipes the local message cache for one or all accounts and resets
// folder sync state so the daemon re-fetches everything from IMAP on its next
// pass. Use this after schema changes that require re-populating existing rows
// (e.g. adding message_id / in_reply_to).
func runResync(ctx context.Context, args []string) error {
	var emails []string
	if len(args) == 1 {
		emails = []string{args[0]}
	} else {
		var err error
		emails, err = configuredAccounts()
		if err != nil {
			return err
		}
		if len(emails) == 0 {
			return fmt.Errorf("no accounts configured — run whkmail setup first")
		}
	}

	fmt.Println()
	fmt.Println("  " + styleBanner.Render("  whkmail resync  "))
	fmt.Println()
	fmt.Println("  This wipes the local message cache. Bodies will be re-downloaded on open.")
	fmt.Println()

	for _, email := range emails {
		if err := resyncAccount(ctx, email); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s: %v\n", styleErr.Render("!"), email, err)
		}
	}

	fmt.Println()
	fmt.Println("  " + styleMuted.Render("Restart the daemon to trigger a fresh sync:"))
	fmt.Println("  " + styleStep.Render("systemctl --user restart whkmaild"))
	fmt.Println()
	return nil
}

func resyncAccount(ctx context.Context, email string) error {
	dbPath := dirs.AccountDBFile(email)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "  %s %s: no database found, skipping\n", styleMuted.Render("·"), email)
		return nil
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, `DELETE FROM messages`); err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE folders SET uid_validity=0, uid_next=1`); err != nil {
		return fmt.Errorf("reset folder sync: %w", err)
	}

	fmt.Printf("  %s %s: cache cleared\n", styleOK.Render("✓"), email)
	return nil
}

func configuredAccounts() ([]string, error) {
	raw, err := os.ReadFile(dirs.ConfigFile())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	accounts := cfg.ResolvedAccounts()
	emails := make([]string, len(accounts))
	for i, a := range accounts {
		emails[i] = a.Email
	}
	return emails, nil
}
