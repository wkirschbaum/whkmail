package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	mailsync "github.com/wkirschbaum/whkmail/internal/sync"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// checkSetup validates that all required config and auth files are present.
// It prints a clear, human-readable error and exits if anything is missing.
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

`, dirs.ConfigFile())
		missing = true
	}

	credFile := dirs.ConfigDir() + "/credentials.json"
	if _, err := os.Stat(credFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, `whkmaild: missing credentials.json

  Download your OAuth2 credentials from Google Cloud Console:
    APIs & Services → Credentials → Create OAuth client ID → Desktop app
  Save the downloaded file as:
    %s

  Then run:  whkmail auth

`, credFile)
		missing = true
	}

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

// loadConfig reads config.json and restores the saved OAuth2 token.
func loadConfig(ctx context.Context) (types.Config, func(context.Context) (string, error), error) {
	raw, err := os.ReadFile(dirs.ConfigFile())
	if err != nil {
		return types.Config{}, nil, fmt.Errorf("read config: %w", err)
	}
	var cfg types.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return types.Config{}, nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.IMAPHost == "" || cfg.IMAPPort == 0 || cfg.Email == "" {
		return types.Config{}, nil, fmt.Errorf("config.json is missing required fields (imap_host, imap_port, email)")
	}

	credFile := dirs.ConfigDir() + "/credentials.json"
	b, err := os.ReadFile(credFile)
	if err != nil {
		return cfg, nil, fmt.Errorf("read credentials.json: %w", err)
	}
	oauthCfg, err := google.ConfigFromJSON(b, mailsync.GmailIMAPScope)
	if err != nil {
		return cfg, nil, fmt.Errorf("parse credentials.json: %w", err)
	}

	tok, err := loadToken()
	if err != nil {
		return cfg, nil, fmt.Errorf("load token: %w", err)
	}

	ts := oauthCfg.TokenSource(ctx, tok)
	tokenFn := func(ctx context.Context) (string, error) {
		t, err := ts.Token()
		if err != nil {
			return "", err
		}
		if err := saveToken(t); err != nil {
			slog.Warn("failed to persist refreshed token", "err", err)
		}
		return t.AccessToken, nil
	}

	return cfg, tokenFn, nil
}

func loadToken() (*oauth2.Token, error) {
	b, err := os.ReadFile(dirs.TokenFile())
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveToken(tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return os.WriteFile(dirs.TokenFile(), b, 0o600)
}
