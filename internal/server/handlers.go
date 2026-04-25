package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/wkirschbaum/whkmail/internal/smtp"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// BuildMux wires every handler onto a fresh mux. Exposed so tests can mount
// the same routing that Serve runs in production.
func BuildMux(st *State) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", st.HandleStatus)
	mux.HandleFunc("GET /accounts/{account}/folders/{folder}/messages", st.HandleMessages)
	mux.HandleFunc("GET /accounts/{account}/folders/{folder}/messages/{uid}", st.HandleMessage)
	mux.HandleFunc("POST /accounts/{account}/folders/{folder}/messages/{uid}/read", st.HandleMarkRead)
	mux.HandleFunc("POST /accounts/{account}/folders/{folder}/messages/{uid}/unread", st.HandleMarkUnread)
	mux.HandleFunc("POST /accounts/{account}/folders/{folder}/messages/{uid}/trash", st.HandleTrash)
	mux.HandleFunc("POST /accounts/{account}/folders/{folder}/messages/{uid}/delete", st.HandlePermanentDelete)
	mux.HandleFunc("POST /accounts/{account}/folders/{folder}/messages/{uid}/move", st.HandleMove)
	mux.HandleFunc("POST /accounts/{account}/send", st.HandleSend)
	mux.HandleFunc("DELETE /accounts/{account}", st.HandleRemoveAccount)
	mux.HandleFunc("GET /events", st.handleSSE)
	return mux
}

// HandleStatus lists every registered account with its folders and current
// sync state. Called by the TUI at startup and whenever KindSyncDone fires.
func (st *State) HandleStatus(w http.ResponseWriter, r *http.Request) {
	// Snapshot the accounts under the read lock so a concurrent RemoveAccount
	// can't mutate the map mid-iteration.
	snapshot := st.snapshotAccounts()

	statuses := make([]types.AccountStatus, 0, len(snapshot))
	for _, ac := range snapshot {
		folders, err := ac.store.ListFolders(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		statuses = append(statuses, types.AccountStatus{
			Account: ac.email,
			Syncing: ac.syncing.Load(),
			Folders: folders,
		})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Account < statuses[j].Account })
	writeJSON(w, types.StatusResponse{Accounts: statuses})
}

// HandleMessages returns the cached message list for a folder — headers only,
// no bodies. The TUI pages it with the viewport; capped at 200 entries.
func (st *State) HandleMessages(w http.ResponseWriter, r *http.Request) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	folder := r.PathValue("folder")
	msgs, err := ac.store.ListMessages(r.Context(), folder, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, types.MessagesResponse{Folder: folder, Messages: msgs, Total: len(msgs)})
}

// HandleMessage returns one cached message. When the body hasn't been
// downloaded yet, a background job is queued and the worker publishes
// KindBodyReady on completion — the TUI re-requests on that event.
func (st *State) HandleMessage(w http.ResponseWriter, r *http.Request) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	folder := r.PathValue("folder")
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	msg, err := ac.store.GetMessage(r.Context(), folder, uint32(uid))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg == nil {
		http.NotFound(w, r)
		return
	}

	// Body not yet fetched — enqueue a background fetch and return immediately.
	if !msg.BodyFetched && ac.provider != nil {
		st.enqueueBodyFetch(ac.email, folder, uint32(uid))
	}

	writeJSON(w, types.MessageResponse{Message: *msg})
}

// HandleMarkRead flags a single message as seen on the server and in the
// local cache. Invoked by the TUI after the user has kept the message open
// for the configured delay.
func (st *State) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	st.mutateMessage(w, r, func(ac *accountState, folder string, uid uint32) error {
		ctx, cancel := context.WithTimeout(r.Context(), OpRequestTimeout)
		defer cancel()
		return ac.provider.MarkRead(ctx, folder, uid)
	})
}

// HandleMarkUnread clears the \Seen flag on the server and in the local
// cache. Invoked by the TUI when the user explicitly marks a message unread.
func (st *State) HandleMarkUnread(w http.ResponseWriter, r *http.Request) {
	st.mutateMessage(w, r, func(ac *accountState, folder string, uid uint32) error {
		ctx, cancel := context.WithTimeout(r.Context(), OpRequestTimeout)
		defer cancel()
		return ac.provider.MarkUnread(ctx, folder, uid)
	})
}

// HandleTrash enqueues a message for background trashing. Enqueue blocks
// until a slot is free (backpressure) or the request context expires (503).
// On success the local cache row is removed immediately so a concurrent sync
// cannot resurrect the message before the IMAP MOVE completes. Returns 202.
func (st *State) HandleTrash(w http.ResponseWriter, r *http.Request) {
	st.enqueueMutation(w, r, st.enqueueTrash)
}

// HandlePermanentDelete enqueues a message for background expunge. Same
// async + backpressure semantics as HandleTrash.
func (st *State) HandlePermanentDelete(w http.ResponseWriter, r *http.Request) {
	st.enqueueMutation(w, r, st.enqueuePermanentDelete)
}

// HandleMove moves a message from its current folder to a target folder in
// one inline IMAP UID MOVE. The target folder name is passed as a JSON body
// {"target":"..."} so arbitrary folder names (including those containing
// slashes) round-trip cleanly without extra URL encoding.
func (st *State) HandleMove(w http.ResponseWriter, r *http.Request) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	if ac.provider == nil {
		http.Error(w, "no provider", http.StatusServiceUnavailable)
		return
	}
	folder := r.PathValue("folder")
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	var req struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, "invalid body: expected {\"target\":\"<folder>\"}", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), OpRequestTimeout)
	defer cancel()
	if err := ac.provider.MoveToFolder(ctx, folder, req.Target, uint32(uid)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := ac.store.DeleteMessage(r.Context(), folder, uint32(uid)); err != nil {
		slog.Warn("HandleMove: delete from store", "account", ac.email, "folder", folder, "uid", uid, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// enqueueMutation is the shared plumbing for HandleTrash and
// HandlePermanentDelete: validates the request, calls enqueue (blocking with
// the request context for backpressure), removes the local cache row on
// success, and writes 202 Accepted.
func (st *State) enqueueMutation(
	w http.ResponseWriter,
	r *http.Request,
	enqueue func(ctx context.Context, account, folder string, uid uint32) error,
) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	if ac.provider == nil {
		http.Error(w, "no provider", http.StatusServiceUnavailable)
		return
	}
	folder := r.PathValue("folder")
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	if err := enqueue(r.Context(), ac.email, folder, uint32(uid)); err != nil {
		http.Error(w, "queue full: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := ac.store.DeleteMessage(r.Context(), folder, uint32(uid)); err != nil {
		slog.Warn("enqueueMutation: delete from store", "account", ac.email, "folder", folder, "uid", uid, "err", err)
	}
	w.WriteHeader(http.StatusAccepted)
}

// HandleSend delivers a composed message through the account's sender.
// Returns 503 when no sender is configured (e.g. read-only account) so
// the TUI can show a specific error instead of a generic 500.
//
// On success the handler fires a background re-sync of the Sent folder
// (so the new message appears in [Gmail]/Sent Mail right away) and, if
// the TUI passed one, the folder containing the replied-to message (so
// the \Answered flag Gmail sets on the parent propagates without
// waiting for IDLE).
func (st *State) HandleSend(w http.ResponseWriter, r *http.Request) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	if ac.sender == nil {
		http.Error(w, "no sender configured", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	var req types.SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), SendTimeout)
	defer cancel()
	msg := smtp.Message{
		From:       ac.email,
		To:         req.To,
		Cc:         req.Cc,
		Subject:    req.Subject,
		Body:       req.Body,
		InReplyTo:  req.InReplyTo,
		References: req.References,
	}
	if err := ac.sender.Send(ctx, msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Re-sync affected folders in the background — never block the
	// HTTP response on it. The TUI auto-refreshes on the KindSyncDone
	// event SyncFolder emits, so the user sees the sent message and
	// the \Answered flag without pressing a refresh key.
	go st.resyncAfterSend(ac, req.SourceFolder)
	w.WriteHeader(http.StatusNoContent)
}

// resyncAfterSend walks the folders that a successful send could have
// affected and triggers a one-off sync of each. Errors are logged but
// never fatal — the TUI has already accepted the send.
func (st *State) resyncAfterSend(ac *accountState, sourceFolder string) {
	if ac.provider == nil {
		return
	}
	// Derive from serverCtx so an in-progress resync is cancelled on daemon
	// shutdown rather than running for up to SendTimeout after the signal.
	parent := st.serverCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, SendTimeout)
	defer cancel()

	if sourceFolder != "" {
		if err := ac.provider.SyncFolder(ctx, sourceFolder); err != nil {
			slog.Warn("post-send source sync", "account", ac.email, "folder", sourceFolder, "err", err)
		}
	}
	sent, err := ac.provider.ResolveSentFolder(ctx)
	if err != nil {
		slog.Warn("post-send: resolve sent folder", "account", ac.email, "err", err)
		return
	}
	if err := ac.provider.SyncFolder(ctx, sent); err != nil {
		slog.Warn("post-send sent sync", "account", ac.email, "folder", sent, "err", err)
	}
}

// HandleRemoveAccount stops the account's syncer and drops it from the
// running daemon. On-disk cleanup (token, DB, config.json) is the CLI's job —
// the daemon only owns in-memory state.
func (st *State) HandleRemoveAccount(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("account")
	if err := st.RemoveAccount(email); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSSE streams events to the TUI as newline-delimited JSON over an
// SSE connection. It flushes headers immediately so clients see the stream
// open before the first event, and sends periodic keepalive pings so
// intermediate proxies don't drop idle connections.
func (st *State) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := st.bus.Subscribe(32)
	defer st.bus.Unsubscribe(ch)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)

	// Flush headers immediately so the client knows the stream is live
	// before the first event arrives.
	if err := rc.Flush(); err != nil {
		return
	}

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprint(w, "data: "); err != nil {
				return
			}
			if err := enc.Encode(e); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// accountFromRequest resolves the {account} path parameter to an accountState,
// writing a 404 when unknown.
func (st *State) accountFromRequest(w http.ResponseWriter, r *http.Request) (*accountState, bool) {
	email := r.PathValue("account")
	ac := st.lookupAccount(email)
	if ac == nil {
		http.Error(w, "unknown account", http.StatusNotFound)
		return nil, false
	}
	return ac, true
}

// mutateMessage is the shared plumbing for handlers that run a provider
// operation on (account, folder, uid). Centralises URL parsing, provider
// presence check, and the success response.
func (st *State) mutateMessage(w http.ResponseWriter, r *http.Request, op func(ac *accountState, folder string, uid uint32) error) {
	ac, ok := st.accountFromRequest(w, r)
	if !ok {
		return
	}
	if ac.provider == nil {
		http.Error(w, "no provider", http.StatusServiceUnavailable)
		return
	}
	folder := r.PathValue("folder")
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	if err := op(ac, folder, uint32(uid)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error: failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
