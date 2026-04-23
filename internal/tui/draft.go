package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/wkirschbaum/whkmail/internal/dirs"
	"github.com/wkirschbaum/whkmail/internal/types"
)

// Draft persistence for in-progress replies. Drafts live under
//
//	<state>/accounts/<safeEmail>/drafts/<key>.json
//
// where the key is derived from the In-Reply-To Message-ID of the
// original message — stable across TUI restarts so the user can reopen
// a reply and pick up where they left off. After a successful send the
// file is deleted; on cancel it's left in place so `r` on the same
// thread loads it back.
//
// This file is TUI-local state. The daemon never reads it; there's no
// wire protocol for drafts. If the user runs multiple TUIs against the
// same account the last writer wins, which is the same consistency
// guarantee any editor gives you.

// draftKey returns a filesystem-safe identifier for the draft attached
// to this reply parent. Prefers the parent's Message-ID (stable); falls
// back to a hash of the Subject+From when a parent has no ID (e.g.
// old messages that never had one indexed).
func draftKey(orig types.Message) string {
	if id := strings.Trim(orig.MessageID, "<>"); id != "" {
		return hashKey(id)
	}
	return hashKey(orig.From + "\x00" + orig.Subject)
}

func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// draftPath returns the on-disk location for (account, key). Safe to
// call with an empty account — the returned path just lands under a
// blank directory and subsequent I/O errors surface cleanly.
func draftPath(account, key string) string {
	return filepath.Join(dirs.AccountStateDir(account), "drafts", key+".json")
}

// saveDraft writes req to the draft file. Creates the drafts dir on
// first call. Errors bubble up so the TUI can show them — a full disk
// or permission error during auto-save shouldn't be silent.
func saveDraft(account, key string, req types.SendRequest) error {
	path := draftPath(account, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// loadDraft returns the stored draft if any. (nil, nil) means "no draft
// yet" — the caller treats this the same as a fresh compose. Malformed
// files return an error so the user sees the problem rather than
// silently losing content.
func loadDraft(account, key string) (*types.SendRequest, error) {
	b, err := os.ReadFile(draftPath(account, key))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var req types.SendRequest
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// deleteDraft removes the draft file. Missing file is not an error —
// the caller doesn't care whether there was one to start with.
func deleteDraft(account, key string) error {
	err := os.Remove(draftPath(account, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
