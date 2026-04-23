package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wkirschbaum/whkmail/internal/events"
	"github.com/wkirschbaum/whkmail/internal/server"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// fixtureStore implements server.MailStore with a mutable body that is
// initially empty and gets filled in when FetchBody runs — the same race
// the production store creates between list fetch and body warm.
type fixtureStore struct {
	mu        sync.Mutex
	folders   []types.Folder
	msgs      []types.Message
	body      string // current body for the single test message
	getMsgUID uint32
}

func (s *fixtureStore) ListFolders(context.Context) ([]types.Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Folder, len(s.folders))
	copy(out, s.folders)
	return out, nil
}

func (s *fixtureStore) ListMessages(_ context.Context, _ string, _ int) ([]types.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Message, len(s.msgs))
	copy(out, s.msgs)
	return out, nil
}

func (s *fixtureStore) GetMessage(_ context.Context, folder string, uid uint32) (*types.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.msgs {
		if s.msgs[i].Folder == folder && s.msgs[i].UID == uid {
			m := s.msgs[i]
			m.BodyText = s.body
			s.getMsgUID = uid
			return &m, nil
		}
	}
	return nil, nil
}

func (s *fixtureStore) setBody(b string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = b
}

// fixtureProvider records calls and lets tests inject errors / bodies.
type fixtureProvider struct {
	mu          sync.Mutex
	fetchBodyFn func(folder string, uid uint32) (string, error)
	markReadFn  func(folder string, uid uint32) error
	trashFn     func(folder string, uid uint32) error
	deleteFn    func(folder string, uid uint32) error

	markReadCalls []readCall
	trashCalls    []readCall
	deleteCalls   []readCall
}

type readCall struct {
	folder string
	uid    uint32
}

func (p *fixtureProvider) FetchBody(_ context.Context, folder string, uid uint32) (string, error) {
	p.mu.Lock()
	fn := p.fetchBodyFn
	p.mu.Unlock()
	if fn == nil {
		return "", nil
	}
	return fn(folder, uid)
}

func (p *fixtureProvider) MarkRead(_ context.Context, folder string, uid uint32) error {
	p.mu.Lock()
	p.markReadCalls = append(p.markReadCalls, readCall{folder: folder, uid: uid})
	fn := p.markReadFn
	p.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(folder, uid)
}

func (p *fixtureProvider) marks() []readCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]readCall, len(p.markReadCalls))
	copy(out, p.markReadCalls)
	return out
}

func (p *fixtureProvider) Trash(_ context.Context, folder string, uid uint32) error {
	p.mu.Lock()
	p.trashCalls = append(p.trashCalls, readCall{folder: folder, uid: uid})
	fn := p.trashFn
	p.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(folder, uid)
}

func (p *fixtureProvider) PermanentDelete(_ context.Context, folder string, uid uint32) error {
	p.mu.Lock()
	p.deleteCalls = append(p.deleteCalls, readCall{folder: folder, uid: uid})
	fn := p.deleteFn
	p.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(folder, uid)
}

func (p *fixtureProvider) trashes() []readCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]readCall, len(p.trashCalls))
	copy(out, p.trashCalls)
	return out
}

func (p *fixtureProvider) deletes() []readCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]readCall, len(p.deleteCalls))
	copy(out, p.deleteCalls)
	return out
}

const itAccount = "int@example.com"

// newTestBackend spins up a real httptest server using the production mux so
// the Client exercises end-to-end HTTP + routing, not just handler internals.
func newTestBackend(t *testing.T, store server.MailStore, prov server.MailProvider) (*Client, *server.State, func()) {
	t.Helper()
	bus := events.NewBus()
	st := server.NewState(bus)
	st.AddAccount(itAccount, store, prov)
	srv := httptest.NewServer(server.BuildMux(st))
	client := newClient(srv.URL, srv.Client().Transport)
	return client, st, srv.Close
}

func TestIntegration_Status(t *testing.T) {
	store := &fixtureStore{folders: []types.Folder{{Name: "INBOX", Unread: 3}}}
	client, _, done := newTestBackend(t, store, &fixtureProvider{})
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Accounts) != 1 || resp.Accounts[0].Account != itAccount {
		t.Fatalf("unexpected accounts: %+v", resp.Accounts)
	}
	if len(resp.Accounts[0].Folders) != 1 || resp.Accounts[0].Folders[0].Unread != 3 {
		t.Errorf("folder payload unexpected: %+v", resp.Accounts[0].Folders)
	}
}

func TestIntegration_Messages(t *testing.T) {
	store := &fixtureStore{
		msgs: []types.Message{
			{UID: 1, Folder: "INBOX", Subject: "hello"},
			{UID: 2, Folder: "INBOX", Subject: "world"},
		},
	}
	client, _, done := newTestBackend(t, store, &fixtureProvider{})
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Messages(ctx, itAccount, "INBOX")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(resp.Messages) != 2 || resp.Messages[0].Subject != "hello" {
		t.Errorf("unexpected messages: %+v", resp.Messages)
	}
}

func TestIntegration_Message_EnqueuesBodyFetch(t *testing.T) {
	store := &fixtureStore{
		msgs: []types.Message{{UID: 7, Folder: "INBOX", Subject: "no body yet"}},
	}
	fetched := make(chan readCall, 1)
	prov := &fixtureProvider{
		fetchBodyFn: func(folder string, uid uint32) (string, error) {
			body := "body for " + folder
			store.setBody(body)
			fetched <- readCall{folder: folder, uid: uid}
			return body, nil
		},
	}
	client, st, done := newTestBackend(t, store, prov)
	defer done()

	// Kick the worker that Serve would normally run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Worker(ctx)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	resp, err := client.Message(ctx2, itAccount, "INBOX", 7)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	// First GET returns the cached-but-bodyless message and enqueues a fetch.
	if resp.Message.BodyText != "" {
		t.Errorf("first GET should return empty body, got %q", resp.Message.BodyText)
	}

	select {
	case c := <-fetched:
		if c.folder != "INBOX" || c.uid != 7 {
			t.Errorf("unexpected fetch call: %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not pick up the body fetch job")
	}

	// Next GET should return the freshly-loaded body.
	resp2, err := client.Message(ctx2, itAccount, "INBOX", 7)
	if err != nil {
		t.Fatalf("Message (2nd): %v", err)
	}
	if resp2.Message.BodyText != "body for INBOX" {
		t.Errorf("expected cached body, got %q", resp2.Message.BodyText)
	}
}

func TestIntegration_BodyFetchFailure_PublishesError(t *testing.T) {
	store := &fixtureStore{
		msgs: []types.Message{{UID: 9, Folder: "INBOX", Subject: "broken"}},
	}
	prov := &fixtureProvider{
		fetchBodyFn: func(string, uint32) (string, error) {
			return "", errors.New("imap exploded")
		},
	}
	// We need direct bus access to observe the published event; spin the
	// backend by hand instead of using newTestBackend.
	bus := events.NewBus()
	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)

	st := server.NewState(bus)
	st.AddAccount(itAccount, store, prov)
	srv := httptest.NewServer(server.BuildMux(st))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Worker(ctx)

	client := newClient(srv.URL, srv.Client().Transport)
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reqCancel()
	if _, err := client.Message(reqCtx, itAccount, "INBOX", 9); err != nil {
		t.Fatalf("Message: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Kind != events.KindBodyReady {
			t.Fatalf("unexpected kind: %v", ev.Kind)
		}
		if ev.Error == "" {
			t.Fatal("expected non-empty Error on failure event")
		}
		if ev.UID != 9 || ev.Folder != "INBOX" {
			t.Errorf("wrong address on failure event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no failure event published — TUI would hang on Loading…")
	}
}

func TestIntegration_MarkRead(t *testing.T) {
	prov := &fixtureProvider{}
	client, _, done := newTestBackend(t, &fixtureStore{}, prov)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.MarkRead(ctx, itAccount, "INBOX", 42); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	calls := prov.marks()
	if len(calls) != 1 || calls[0].folder != "INBOX" || calls[0].uid != 42 {
		t.Errorf("unexpected MarkRead calls: %+v", calls)
	}
}

func TestIntegration_MarkRead_Error(t *testing.T) {
	prov := &fixtureProvider{
		markReadFn: func(string, uint32) error { return errors.New("boom") },
	}
	client, _, done := newTestBackend(t, &fixtureStore{}, prov)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := client.MarkRead(ctx, itAccount, "INBOX", 42)
	if err == nil {
		t.Fatal("expected error from MarkRead")
	}
}

func TestIntegration_Trash(t *testing.T) {
	prov := &fixtureProvider{}
	client, _, done := newTestBackend(t, &fixtureStore{}, prov)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Trash(ctx, itAccount, "INBOX", 17); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	calls := prov.trashes()
	if len(calls) != 1 || calls[0].folder != "INBOX" || calls[0].uid != 17 {
		t.Errorf("unexpected Trash calls: %+v", calls)
	}
}

func TestIntegration_PermanentDelete(t *testing.T) {
	prov := &fixtureProvider{}
	client, _, done := newTestBackend(t, &fixtureStore{}, prov)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.PermanentDelete(ctx, itAccount, "[Gmail]/Trash", 99); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}
	calls := prov.deletes()
	if len(calls) != 1 || calls[0].folder != "[Gmail]/Trash" || calls[0].uid != 99 {
		t.Errorf("unexpected PermanentDelete calls: %+v", calls)
	}
}

func TestIntegration_Message_UnknownAccount(t *testing.T) {
	client, _, done := newTestBackend(t, &fixtureStore{}, &fixtureProvider{})
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Message(ctx, "ghost@example.com", "INBOX", 1)
	if err == nil {
		t.Fatal("expected error for unknown account")
	}
	// Surface the status code via the client's error string.
	if err.Error() == "" || err.Error() == "EOF" {
		t.Errorf("unexpected error text: %v", err)
	}
}

// Compile-time check: fixtureStore + fixtureProvider satisfy the server interfaces.
var _ server.MailStore = (*fixtureStore)(nil)
var _ server.MailProvider = (*fixtureProvider)(nil)
var _ http.RoundTripper = http.DefaultTransport
