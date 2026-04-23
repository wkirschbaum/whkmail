package imap

import "fmt"

// xoauth2 implements sasl.Client for the XOAUTH2 mechanism used by Gmail IMAP.
// The OAuth2 scope string lives in the oauth package (GmailScope) because it
// also covers SMTP.
type xoauth2 struct {
	email string
	token string
}

func (m *xoauth2) Start() (string, []byte, error) {
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", m.email, m.token)
	return "XOAUTH2", []byte(payload), nil
}

func (m *xoauth2) Next(_ []byte) ([]byte, error) {
	// XOAUTH2 is a single-step mechanism; no further challenge is expected.
	return nil, nil
}
