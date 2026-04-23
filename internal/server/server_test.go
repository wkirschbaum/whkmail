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

const testAccount = "test@example.com"

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
func (s *stubStore) DeleteMessage(_ context.Context, _ string, _ uint32) error { return s.err }

func newState(st server.MailStore) *server.State {
	s := server.NewState(events.NewBus())
	s.AddAccount(testAccount, st, nil)
	return s
}

func newStateWithProvider(st server.MailStore, p server.MailProvider) *server.State {
	s := server.NewState(events.NewBus())
	s.AddAccount(testAccount, st, p)
	return s
}

type stubProvider struct {
	fetchBody   func(ctx context.Context, folder string, uid uint32) (string, error)
	markRead    func(ctx context.Context, folder string, uid uint32) error
	markReadArg struct {
		folder string
		uid    uint32
		called bool
	}
}

func (p *stubProvider) FetchBody(ctx context.Context, folder string, uid uint32) (string, error) {
	if p.fetchBody != nil {
		return p.fetchBody(ctx, folder, uid)
	}
	return "", nil
}

func (p *stubProvider) MarkRead(ctx context.Context, folder string, uid uint32) error {
	p.markReadArg.folder = folder
	p.markReadArg.uid = uid
	p.markReadArg.called = true
	if p.markRead != nil {
		return p.markRead(ctx, folder, uid)
	}
	return nil
}

func (p *stubProvider) MarkUnread(context.Context, string, uint32) error         { return nil }
func (p *stubProvider) Trash(context.Context, string, uint32) error              { return nil }
func (p *stubProvider) TrashBatch(context.Context, string, []uint32) error       { return nil }
func (p *stubProvider) PermanentDelete(context.Context, string, uint32) error    { return nil }
func (p *stubProvider) SyncFolder(context.Context, string) error              { return nil }
func (p *stubProvider) ResolveSentFolder(context.Context) (string, error) {
	return "[Gmail]/Sent Mail", nil
}

func postRead(account, folder, uid string) *http.Request {
	r := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/accounts/"+account+"/folders/"+folder+"/messages/"+uid+"/read",
		nil,
	)
	r.SetPathValue("account", account)
	r.SetPathValue("folder", folder)
	r.SetPathValue("uid", uid)
	return r
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
	if len(resp.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(resp.Accounts))
	}
	if len(resp.Accounts[0].Folders) != 1 || resp.Accounts[0].Folders[0].Name != "INBOX" {
		t.Errorf("unexpected folders: %+v", resp.Accounts[0].Folders)
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
	r := get("/accounts/" + testAccount + "/folders/INBOX/messages")
	r.SetPathValue("account", testAccount)
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
	r := get("/accounts/" + testAccount + "/folders/INBOX/messages/7")
	r.SetPathValue("account", testAccount)
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
	r := get("/accounts/" + testAccount + "/folders/INBOX/messages/99")
	r.SetPathValue("account", testAccount)
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
	r := get("/accounts/" + testAccount + "/folders/INBOX/messages/bad")
	r.SetPathValue("account", testAccount)
	r.SetPathValue("folder", "INBOX")
	r.SetPathValue("uid", "bad")
	st.HandleMessage(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestHandleMarkRead_OK(t *testing.T) {
	prov := &stubProvider{}
	st := newStateWithProvider(&stubStore{}, prov)

	rec := httptest.NewRecorder()
	st.HandleMarkRead(rec, postRead(testAccount, "INBOX", "42"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}
	if !prov.markReadArg.called {
		t.Fatal("provider.MarkRead was not called")
	}
	if prov.markReadArg.folder != "INBOX" || prov.markReadArg.uid != 42 {
		t.Errorf("unexpected args: %+v", prov.markReadArg)
	}
}

func TestHandleMarkRead_ProviderError(t *testing.T) {
	prov := &stubProvider{
		markRead: func(context.Context, string, uint32) error { return errors.New("imap down") },
	}
	st := newStateWithProvider(&stubStore{}, prov)

	rec := httptest.NewRecorder()
	st.HandleMarkRead(rec, postRead(testAccount, "INBOX", "42"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}

func TestHandleMarkRead_MissingProvider(t *testing.T) {
	st := newState(&stubStore{}) // AddAccount with nil provider

	rec := httptest.NewRecorder()
	st.HandleMarkRead(rec, postRead(testAccount, "INBOX", "42"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestHandleMarkRead_InvalidUID(t *testing.T) {
	st := newStateWithProvider(&stubStore{}, &stubProvider{})

	rec := httptest.NewRecorder()
	st.HandleMarkRead(rec, postRead(testAccount, "INBOX", "not-a-number"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestHandleMarkRead_UnknownAccount(t *testing.T) {
	st := newStateWithProvider(&stubStore{}, &stubProvider{})

	rec := httptest.NewRecorder()
	st.HandleMarkRead(rec, postRead("stranger@example.com", "INBOX", "1"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestRemoveAccount_CancelsAndDeregisters(t *testing.T) {
	called := false
	cancel := func() { called = true }

	st := server.NewState(events.NewBus())
	st.AddAccount(testAccount, &stubStore{}, nil, server.WithCancel(cancel))

	if err := st.RemoveAccount(testAccount); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if !called {
		t.Error("WithCancel function was not invoked")
	}
	// Subsequent handler call should treat it as unknown.
	rec := httptest.NewRecorder()
	r := get("/accounts/" + testAccount + "/folders/INBOX/messages")
	r.SetPathValue("account", testAccount)
	r.SetPathValue("folder", "INBOX")
	st.HandleMessages(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Errorf("post-remove handler: got %d, want 404", rec.Code)
	}
}

func TestRemoveAccount_Unknown(t *testing.T) {
	st := server.NewState(events.NewBus())
	if err := st.RemoveAccount("nobody@example.com"); err == nil {
		t.Error("expected error for unknown account")
	}
}

func TestHandleRemoveAccount(t *testing.T) {
	st := newState(&stubStore{})
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(
		context.Background(), http.MethodDelete, "/accounts/"+testAccount, nil)
	r.SetPathValue("account", testAccount)
	st.HandleRemoveAccount(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", rec.Code)
	}
}

func TestHandleMessage_UnknownAccount(t *testing.T) {
	st := newState(&stubStore{msg: &types.Message{UID: 1}})
	rec := httptest.NewRecorder()
	r := get("/accounts/nobody@example.com/folders/INBOX/messages/1")
	r.SetPathValue("account", "nobody@example.com")
	r.SetPathValue("folder", "INBOX")
	r.SetPathValue("uid", "1")
	st.HandleMessage(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}
