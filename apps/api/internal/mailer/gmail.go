package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const GmailSendScope = "https://www.googleapis.com/auth/gmail.send"
const gmailSendURL = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"

// GmailConfig belongs to the operator's sender mailbox, never a visitor identity.
// Authorize the refresh credential separately with gmail.send only.
type GmailConfig struct{ ClientID, ClientSecret, RefreshToken, From string }
type GmailMailSender struct {
	from   string
	oauth  oauth2.Config
	token  *oauth2.Token
	gate   chan struct{}
	client *http.Client
}

func NewGmailMailSender(c GmailConfig) (*GmailMailSender, error) {
	if c.ClientID == "" || c.ClientSecret == "" || c.RefreshToken == "" || !validAddress(c.From) {
		return nil, errors.New("incomplete Gmail sender configuration")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &GmailMailSender{
		from: c.From, client: client, gate: make(chan struct{}, 1),
		oauth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: []string{GmailSendScope},
			Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}},
		token: &oauth2.Token{RefreshToken: c.RefreshToken},
	}, nil
}
func (s *GmailMailSender) accessToken(ctx context.Context) (*oauth2.Token, error) {
	// Context-aware locking bounds concurrent refreshes and shares the in-memory
	// access token. Neither tokens nor provider errors leave this adapter.
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrDelivery
	}
	defer func() { <-s.gate }()
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, s.client)
	token, err := s.oauth.TokenSource(refreshCtx, s.token).Token()
	if err != nil {
		return nil, ErrDelivery
	}
	s.token = token
	copy := *token
	return &copy, nil
}
func (s *GmailMailSender) Send(ctx context.Context, m Message) error {
	message, err := encodeMessage(s.from, m)
	if err != nil {
		return err
	}
	defer clear(message)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	token, err := s.accessToken(ctx)
	if err != nil {
		return ErrDelivery
	}
	payload, err := json.Marshal(map[string]string{"raw": base64.RawURLEncoding.EncodeToString(message)})
	if err != nil {
		return ErrMessage
	}
	defer clear(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gmailSendURL, bytes.NewReader(payload))
	if err != nil {
		return ErrDelivery
	}
	token.SetAuthHeader(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return ErrDelivery
	}
	defer response.Body.Close()
	// Response bodies can contain addresses or credentials. Discard a bounded
	// amount, never return/wrap/log provider errors. No automatic send retries:
	// an ambiguous response could otherwise cause duplicate email delivery.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrDelivery
	}
	return nil
}
