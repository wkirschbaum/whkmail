package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// MailStore is the persistence interface required by the HTTP handlers.
type MailStore interface {
	ListFolders(ctx context.Context) ([]types.Folder, error)
	ListMessages(ctx context.Context, folder string, limit int) ([]types.Message, error)
	GetMessage(ctx context.Context, folder string, uid uint32) (*types.Message, error)
}

// State is shared between all HTTP handlers.
type State struct {
	Store   MailStore
	Bus     *events.Bus
	Syncing atomic.Bool
}

func Serve(ctx context.Context, st *State) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", st.HandleStatus)
	mux.HandleFunc("GET /folders/{folder}/messages", st.HandleMessages)
	mux.HandleFunc("GET /folders/{folder}/messages/{uid}", st.HandleMessage)
	mux.HandleFunc("GET /events", st.handleSSE)

	sockPath := dirs.SocketFile()
	// Remove any stale socket left by a previous crash.
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

func (st *State) HandleStatus(w http.ResponseWriter, r *http.Request) {
	folders, err := st.Store.ListFolders(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, types.StatusResponse{
		Syncing: st.Syncing.Load(),
		Folders: folders,
	})
}

func (st *State) HandleMessages(w http.ResponseWriter, r *http.Request) {
	folder := r.PathValue("folder")
	msgs, err := st.Store.ListMessages(r.Context(), folder, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, types.MessagesResponse{Folder: folder, Messages: msgs, Total: len(msgs)})
}

func (st *State) HandleMessage(w http.ResponseWriter, r *http.Request) {
	folder := r.PathValue("folder")
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		http.Error(w, "invalid uid", http.StatusBadRequest)
		return
	}
	msg, err := st.Store.GetMessage(r.Context(), folder, uint32(uid))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, types.MessageResponse{Message: *msg})
}

// handleSSE streams events to the TUI as newline-delimited JSON.
func (st *State) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := st.Bus.Subscribe(32)
	defer st.Bus.Unsubscribe(ch)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			// Write errors mean the client disconnected; let the context handle cleanup.
			//nolint:errcheck
			fmt.Fprint(w, "data: ")
			//nolint:errcheck
			enc.Encode(e)
			//nolint:errcheck
			fmt.Fprint(w, "\n")
			//nolint:errcheck
			rc.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck
	json.NewEncoder(w).Encode(v)
}

