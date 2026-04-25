package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/imap"
	"github.com/wkirschbaum/whkmail/internal/notify"
	"github.com/wkirschbaum/whkmail/internal/server"
	"github.com/wkirschbaum/whkmail/internal/smtp"
	"github.com/wkirschbaum/whkmail/internal/storage"
)

// Gmail SMTP submission endpoint. Uses STARTTLS on port 587.
const (
	gmailSMTPHost = "smtp.gmail.com"
	gmailSMTPPort = 587
)

// resolveDBPath returns the database path to use for an account.
// Prefers the account-scoped path; falls back to the legacy single-account
// path for existing installations so the message cache is not lost on upgrade.
func resolveDBPath(email string) string {
	accountDB := dirs.AccountDBFile(email)
	if _, err := os.Stat(accountDB); err == nil {
		return accountDB
	}
	if _, err := os.Stat(dirs.DBFile()); err == nil {
		slog.Info("using legacy database", "account", email, "path", dirs.DBFile())
		return dirs.DBFile()
	}
	return accountDB
}

func main() {
	checkSetup()

	// Write logs to both stderr (for journald / terminal) and a persistent
	// file so post-hoc timing inspection is easy.
	if err := os.MkdirAll(dirs.StateDir(), 0o700); err != nil {
		slog.Error("create state dir (pre-log)", "err", err)
		os.Exit(1)
	}
	logF, err := os.OpenFile(dirs.LogFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("open log file", "path", dirs.LogFile(), "err", err)
		os.Exit(1)
	}
	defer func() { _ = logF.Close() }()
	logOpts := &slog.HandlerOptions{Level: slog.LevelInfo}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logF), logOpts)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("whkmaild starting")

	lockF, err := acquireLock()
	if err != nil {
		slog.Error("acquire lock", "err", err)
		os.Exit(1)
	}
	if lockF != nil {
		defer func() { _ = lockF.Close() }()
	}

	bus := events.NewBus()

	notifier, err := notify.NewPlatform()
	if err != nil {
		slog.Warn("notifications unavailable", "err", err)
	} else {
		go notify.Run(ctx, bus, notifier)
	}

	accounts, err := loadConfig(ctx)
	if err != nil {
		slog.Error("load config/auth", "err", err)
		os.Exit(1)
	}

	st := server.NewState(bus)

	// dbs collects open stores so they can be closed after all syncers have
	// stopped. Closing them inside the loop with defer would race: defers run
	// when main returns, but syncer goroutines might still be writing to the DB.
	var (
		dbs       []*storage.SQLite
		syncerWG  sync.WaitGroup
	)

	for _, acc := range accounts {
		accountDir := dirs.AccountStateDir(acc.config.Email)
		if err := os.MkdirAll(accountDir, 0o700); err != nil {
			slog.Error("create account dir", "account", acc.config.Email, "err", err)
			os.Exit(1)
		}

		db, err := storage.OpenSQLite(resolveDBPath(acc.config.Email))
		if err != nil {
			slog.Error("open db", "account", acc.config.Email, "err", err)
			os.Exit(1)
		}
		dbs = append(dbs, db)

		syncer := imap.New(acc.config.IMAPHost, acc.config.IMAPPort, acc.config.Email, acc.tokenFn, db, bus)
		sender := smtp.New(gmailSMTPHost, gmailSMTPPort, acc.config.Email, acc.tokenFn)
		accCtx, accCancel := context.WithCancel(ctx)
		st.AddAccount(acc.config.Email, db, syncer,
			server.WithCancel(accCancel),
			server.WithSender(sender),
		)
		syncerWG.Add(1)
		go func() {
			defer syncerWG.Done()
			syncer.Run(accCtx)
		}()
	}

	if err := server.Serve(ctx, st); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}

	// Wait for all syncer goroutines to drain before closing their databases.
	syncerWG.Wait()
	for _, db := range dbs {
		if err := db.Close(); err != nil {
			slog.Warn("close db", "err", err)
		}
	}
}
