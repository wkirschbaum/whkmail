package smtp

import (
	"fmt"
	"net/smtp"
)

// xoauth2Auth implements smtp.Auth for the SASL XOAUTH2 mechanism, which
// Gmail requires for SMTP submission. It mirrors the IMAP xoauth2 helper
// in internal/imap but the interfaces are different: net/smtp wants
// Start/Next, so we implement those directly without dragging in the
// emersion/go-sasl package just for this one mechanism.
type xoauth2Auth struct {
	email string
	token string
}

// NewXOAUTH2 returns an smtp.Auth that authenticates as email with the
// given access token. The token is expected to already be valid; the
// caller (Sender) is responsible for refreshing via oauth.TokenFn before
// starting a submission.
func NewXOAUTH2(email, token string) smtp.Auth {
	return &xoauth2Auth{email: email, token: token}
}

func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, fmt.Errorf("xoauth2: refusing to send credentials over a non-TLS connection")
	}
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.email, a.token)
	return "XOAUTH2", []byte(payload), nil
}

// Next is only called if the server keeps the SASL exchange going. XOAUTH2
// is single-step on success; if the server sends another challenge it
// means the token was rejected, in which case we surface the error
// message instead of trying to continue.
func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("xoauth2: server rejected token: %s", fromServer)
	}
	return nil, nil
}
