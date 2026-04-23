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

// trashBatchSize caps how many UIDs go into a single IMAP UID MOVE command.
// Gmail has no documented limit but very long UID sets can hit server-side
// command-length guards; 100 is conservative and still gives >100× speedup
// over per-message moves.
const trashBatchSize = 100

// Timeouts for daemon-side work. Kept generous so Gmail's slower edge
// cases (big HTML bodies, high-latency mobile links) don't time out
// mid-operation, but bounded so a wedged IMAP session can't stall a
// handler forever.
const (
	// OpRequestTimeout bounds one-shot IMAP operations invoked from an
	// HTTP handler (MarkRead / MarkUnread / Trash / PermanentDelete).
	OpRequestTimeout = 30 * time.Second

	// BodyFetchTimeout bounds one background body-fetch job. Longer than
	// OpRequestTimeout because bodies can be multi-MB and Gmail attaches
	// MIME parts liberally.
	BodyFetchTimeout = 60 * time.Second

	// ShutdownGrace is how long Serve waits for in-flight HTTP handlers
	// to finish when ctx is cancelled before it force-closes.
	ShutdownGrace = 5 * time.Second
)

// Serve binds the Unix socket, starts the background goroutines, and blocks
// until ctx is cancelled.
func Serve(ctx context.Context, st *State) error {
	go st.trackSyncState(ctx)
	go st.Worker(ctx)
	go st.TrashWorker(ctx)

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
		shutCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
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
	eventCh := st.bus.Subscribe(32)
	defer st.bus.Unsubscribe(eventCh)
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
			st.bus.Publish(st.runBodyFetch(j))
		}
	}
}

// runBodyFetch executes one body-fetch job and returns the event to publish.
// Always returns an event (success or failure) so a TUI waiting on the body
// never hangs on a silently-dropped job.
func (st *State) runBodyFetch(j job) events.Event {
	reason := ""
	ac := st.lookupAccount(j.account)
	switch {
	case ac == nil:
		reason = "unknown account"
	case ac.provider == nil:
		reason = "no provider configured"
	default:
		fetchCtx, cancel := context.WithTimeout(context.Background(), BodyFetchTimeout)
		_, err := ac.provider.FetchBody(fetchCtx, j.folder, j.uid)
		cancel()
		if err != nil {
			slog.Warn("worker: fetch body", "account", j.account, "uid", j.uid, "err", err)
			reason = err.Error()
		}
	}
	return events.BodyReadyEvent(j.account, j.folder, j.uid, reason)
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

// TrashWorker drains trashJobs in the background, batching UIDs that share
// the same (account, folder) into a single IMAP UID MOVE command. This
// transforms N sequential round-trips (one per message) into at most
// ceil(N/trashBatchSize) commands per folder — critical for bulk deletes.
//
// The worker collects the first available job then drains the channel for a
// short window before issuing IMAP commands. Jobs arriving during that window
// are merged into the same batch, so a TUI that fires 100 trash requests in
// rapid succession pays roughly one IMAP command instead of 100.
//
// Exported so tests can drive it directly without going through Serve.
func (st *State) TrashWorker(ctx context.Context) {
	type folderKey struct{ account, folder string }
	const drainWindow = 100 * time.Millisecond

	for {
		var first trashJob
		select {
		case <-ctx.Done():
			return
		case j, ok := <-st.trashJobs:
			if !ok {
				return
			}
			first = j
		}

		batch := map[folderKey][]uint32{
			{first.account, first.folder}: {first.uid},
		}
		timer := time.NewTimer(drainWindow)
	draining:
		for {
			select {
			case j, ok := <-st.trashJobs:
				if !ok {
					timer.Stop()
					break draining
				}
				k := folderKey{j.account, j.folder}
				batch[k] = append(batch[k], j.uid)
			case <-timer.C:
				break draining
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}

		for k, uids := range batch {
			ac := st.lookupAccount(k.account)
			if ac == nil || ac.provider == nil {
				continue
			}
			for len(uids) > 0 {
				chunk := uids
				if len(chunk) > trashBatchSize {
					chunk, uids = uids[:trashBatchSize], uids[trashBatchSize:]
				} else {
					uids = nil
				}
				opCtx, cancel := context.WithTimeout(ctx, OpRequestTimeout)
				if err := ac.provider.TrashBatch(opCtx, k.folder, chunk); err != nil {
					slog.Warn("trash worker", "account", k.account, "folder", k.folder, "count", len(chunk), "err", err)
				}
				cancel()
			}
		}
	}
}

// enqueueTrash queues a trash operation for the background worker. The local
// cache row must already be removed by the caller before enqueuing.
func (st *State) enqueueTrash(account, folder string, uid uint32) {
	select {
	case st.trashJobs <- trashJob{account: account, folder: folder, uid: uid}:
	default:
		slog.Warn("trash queue full, dropping", "account", account, "uid", uid)
	}
}
