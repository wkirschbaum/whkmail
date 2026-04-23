package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/oauth"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// checkSetup validates that the required config and auth files are present.
// It prints human-readable errors and exits if anything is missing.
// For multi-account setups with account-scoped credentials, these checks are
// best-effort — per-account errors surface later in loadConfig.
func checkSetup() {
	missing := false

	if _, err := os.Stat(dirs.ConfigFile()); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, `
whkmaild: missing config file

  Create %s with your Gmail account details:

  {
    "imap_host": "imap.gmail.com",
    "imap_port": 993,
    "email":     "you@gmail.com"
  }

  Or for multiple accounts:

  {
    "accounts": [
      {"email": "you@gmail.com", "imap_host": "imap.gmail.com", "imap_port": 993},
      {"email": "work@gmail.com", "imap_host": "imap.gmail.com", "imap_port": 993}
    ]
  }

  Then run:  whkmail auth

`, dirs.ConfigFile())
		missing = true
	}

	if _, err := os.Stat(dirs.CredentialsFile()); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, `whkmaild: missing credentials.json

  Download your OAuth2 credentials from Google Cloud Console:
    APIs & Services → Credentials → Create OAuth client ID → Desktop app
  Save the downloaded file as:
    %s

  Then run:  whkmail auth

`, dirs.CredentialsFile())
		missing = true
	}

	// Check for any token: account-scoped tokens are tried by loadConfig, so
	// here we only flag the case where the legacy shared token is also absent.
	if _, err := os.Stat(dirs.TokenFile()); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, `whkmaild: not authorized yet

  Run the following command to authorize Gmail access:
    whkmail auth

`)
		missing = true
	}

	if missing {
		os.Exit(1)
	}
}

// loadedAccount bundles a resolved AccountConfig with its OAuth2 token function.
type loadedAccount struct {
	config  types.AccountConfig
	tokenFn func(context.Context) (string, error)
}

// loadConfig reads config.json and builds a token source for each account.
func loadConfig(ctx context.Context) ([]loadedAccount, error) {
	raw, err := os.ReadFile(dirs.ConfigFile())
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	accs := cfg.ResolvedAccounts()
	if len(accs) == 0 {
		return nil, fmt.Errorf("config.json: no accounts configured")
	}

	var loaded []loadedAccount
	for _, acc := range accs {
		if acc.IMAPHost == "" || acc.IMAPPort == 0 || acc.Email == "" {
			return nil, fmt.Errorf("account %q: missing imap_host, imap_port, or email", acc.Email)
		}
		fn, err := oauth.TokenFn(ctx, acc.Email)
		if err != nil {
			return nil, fmt.Errorf("account %q: %w", acc.Email, err)
		}
		loaded = append(loaded, loadedAccount{config: acc, tokenFn: fn})
	}
	return loaded, nil
}
