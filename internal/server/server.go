// Package server exposes the daemon's in-memory account registry and the
// HTTP handlers that read / mutate it. The lifecycle (Serve) and the body-
// fetch worker live here; registration + lookup are in state.go; the HTTP
// handlers are in handlers.go.
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/events"
)

// Serve binds the Unix socket, starts the background goroutines, and blocks
// until ctx is cancelled.
func Serve(ctx context.Context, st *State) error {
	go st.trackSyncState(ctx)
	go st.Worker(ctx)

	mux := BuildMux(st)

	sockPath := dirs.SocketFile()
	_ = os.Remove(sockPath)

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "unix", sockPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("remove socket", "err", err)
		}
	}()

	slog.Info("daemon listening", "socket", sockPath)

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Warn("shutdown", "err", err)
		}
	}()

	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// trackSyncState watches the bus and toggles each account's `syncing` flag
// so /status reports accurate live state without polling the IMAP client.
func (st *State) trackSyncState(ctx context.Context) {
	eventCh := st.Bus.Subscribe(32)
	defer st.Bus.Unsubscribe(eventCh)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-eventCh:
			if !ok {
				return
			}
			ac := st.lookupAccount(e.Account)
			if ac == nil {
				continue
			}
			switch e.Kind {
			case events.KindSyncStarted:
				ac.syncing.Store(true)
			case events.KindSyncDone:
				ac.syncing.Store(false)
			}
		}
	}
}

// Worker is the background goroutine that fetches message bodies on demand.
// Mark-as-read is intentionally *not* triggered here — the TUI drives that
// explicitly via POST /read after the user has kept a message open long enough.
// It publishes KindBodyReady when a fetch completes so the TUI can update
// without polling. Exported so integration tests can drive it directly.
func (st *State) Worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-st.jobs:
			if !ok {
				return
			}
			st.Bus.Publish(st.runBodyFetch(j))
		}
	}
}

// runBodyFetch executes one body-fetch job and returns the event to publish.
// Always returns an event (success or failure) so a TUI waiting on the body
// never hangs on a silently-dropped job.
func (st *State) runBodyFetch(j job) events.Event {
	ev := events.Event{
		Kind:    events.KindBodyReady,
		Account: j.account,
		Folder:  j.folder,
		UID:     j.uid,
	}
	ac := st.lookupAccount(j.account)
	switch {
	case ac == nil:
		ev.Error = "unknown account"
	case ac.provider == nil:
		ev.Error = "no provider configured"
	default:
		fetchCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, err := ac.provider.FetchBody(fetchCtx, j.folder, j.uid)
		cancel()
		if err != nil {
			slog.Warn("worker: fetch body", "account", j.account, "uid", j.uid, "err", err)
			ev.Error = err.Error()
		}
	}
	return ev
}

// enqueueBodyFetch hands a fetch request to the worker queue. Drops with a
// log line if the buffer is full — under normal load the queue drains faster
// than the TUI can request, so this should only happen during extreme bursts.
func (st *State) enqueueBodyFetch(account, folder string, uid uint32) {
	select {
	case st.jobs <- job{account: account, folder: folder, uid: uid}:
	default:
		slog.Warn("job queue full, dropping fetch", "account", account, "uid", uid)
	}
}
