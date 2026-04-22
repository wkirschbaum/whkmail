package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/server"
	"github.com/wkirschbaum/whkmail/internal/types"
)

type stubStore struct {
	folders []types.Folder
	msgs    []types.Message
	msg     *types.Message
	err     error
}

func (s *stubStore) ListFolders(_ context.Context) ([]types.Folder, error) {
	return s.folders, s.err
}
func (s *stubStore) ListMessages(_ context.Context, _ string, _ int) ([]types.Message, error) {
	return s.msgs, s.err
}
func (s *stubStore) GetMessage(_ context.Context, _ string, _ uint32) (*types.Message, error) {
	return s.msg, s.err
}

func newState(st server.MailStore) *server.State {
	return &server.State{Store: st, Bus: events.NewBus()}
}

func get(target string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
}

func TestHandleStatus_OK(t *testing.T) {
	st := newState(&stubStore{folders: []types.Folder{{Name: "INBOX", Unread: 2}}})
	rec := httptest.NewRecorder()
	st.HandleStatus(rec, get("/status"))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp types.StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Folders) != 1 || resp.Folders[0].Name != "INBOX" {
		t.Errorf("unexpected folders: %+v", resp.Folders)
	}
}

func TestHandleStatus_StoreError(t *testing.T) {
	st := newState(&stubStore{err: errors.New("db error")})
	rec := httptest.NewRecorder()
	st.HandleStatus(rec, get("/status"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}

func TestHandleMessages_OK(t *testing.T) {
	st := newState(&stubStore{msgs: []types.Message{{UID: 1, Subject: "Hi"}}})
	rec := httptest.NewRecorder()
	r := get("/folders/INBOX/messages")
	r.SetPathValue("folder", "INBOX")
	st.HandleMessages(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp types.MessagesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(resp.Messages))
	}
}

func TestHandleMessage_Found(t *testing.T) {
	st := newState(&stubStore{msg: &types.Message{UID: 7, Subject: "Hello"}})
	rec := httptest.NewRecorder()
	r := get("/folders/INBOX/messages/7")
	r.SetPathValue("folder", "INBOX")
	r.SetPathValue("uid", "7")
	st.HandleMessage(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp types.MessageResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message.Subject != "Hello" {
		t.Errorf("subject: got %q, want Hello", resp.Message.Subject)
	}
}

func TestHandleMessage_NotFound(t *testing.T) {
	st := newState(&stubStore{msg: nil})
	rec := httptest.NewRecorder()
	r := get("/folders/INBOX/messages/99")
	r.SetPathValue("folder", "INBOX")
	r.SetPathValue("uid", "99")
	st.HandleMessage(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestHandleMessage_InvalidUID(t *testing.T) {
	st := newState(&stubStore{})
	rec := httptest.NewRecorder()
	r := get("/folders/INBOX/messages/bad")
	r.SetPathValue("folder", "INBOX")
	r.SetPathValue("uid", "bad")
	st.HandleMessage(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}
