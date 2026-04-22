package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/notify"
	"github.com/wkirschbaum/whkmail/internal/server"
	"github.com/wkirschbaum/whkmail/internal/store"
	mailsync "github.com/wkirschbaum/whkmail/internal/sync"
)

func main() {
	checkSetup()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(dirs.StateDir(), 0o700); err != nil {
		slog.Error("create state dir", "err", err)
		os.Exit(1)
	}

	db, err := store.Open(dirs.DBFile())
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("close db", "err", err)
		}
	}()

	bus := events.NewBus()

	notifier, err := notify.NewPlatform()
	if err != nil {
		slog.Warn("notifications unavailable", "err", err)
	} else {
		go notify.Run(ctx, bus, notifier)
	}

	cfg, tokenSrc, err := loadConfig(ctx)
	if err != nil {
		slog.Error("load config/auth", "err", err)
		os.Exit(1)
	}

	st := &server.State{Store: db, Bus: bus}

	syncer := mailsync.New(cfg.IMAPHost, cfg.IMAPPort, cfg.Email, tokenSrc, db, bus)
	go func() {
		st.Syncing.Store(true)
		syncer.Run(ctx)
		st.Syncing.Store(false)
	}()

	if err := server.Serve(ctx, st); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
