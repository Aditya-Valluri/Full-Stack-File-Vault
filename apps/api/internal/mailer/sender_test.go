package mailer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testMessage() Message {
	return Message{To: "recipient@example.com", Purpose: VerifyEmail, Code: "0123456"}
}
func testSender(t *testing.T) *GmailMailSender {
	t.Helper()
	s, err := NewGmailMailSender(GmailConfig{ClientID: "test-id", ClientSecret: "test-secret", RefreshToken: "test-refresh", From: "sender@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestGmailSendAndConcurrentTokenReuse(t *testing.T) {
	s := testSender(t)
	var refreshes, sends atomic.Int32
	if len(s.oauth.Scopes) != 1 || s.oauth.Scopes[0] != GmailSendScope {
		t.Fatal("unexpected sender scopes")
	}
	s.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://oauth2.googleapis.com/token":
			refreshes.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "test-refresh" || r.Form.Get("client_secret") != "test-secret" {
				t.Error("wrong OAuth refresh")
			}
			return response(200, `{"access_token":"test-access","token_type":"Bearer","expires_in":3600}`), nil
		case gmailSendURL:
			sends.Add(1)
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-access" {
				t.Error("incorrect Gmail request")
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			message, err := base64.RawURLEncoding.DecodeString(payload["raw"])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(message, []byte("To: recipient@example.com\r\n")) || !bytes.Contains(message, []byte(testMessage().Code)) {
				t.Error("incorrect MIME payload")
			}
			if len(payload) != 1 || bytes.Contains(message, []byte("test-refresh")) || bytes.Contains(message, []byte("test-secret")) {
				t.Error("credentials leaked in message")
			}
			return response(200, `{"id":"ignored"}`), nil
		default:
			t.Error("unexpected external endpoint")
			return response(500, ""), nil
		}
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Send(context.Background(), testMessage()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 || sends.Load() != 4 {
		t.Fatal("refresh caching or message count failed")
	}
}
func TestGmailErrorsAreSanitizedAndNotRetried(t *testing.T) {
	for _, failAt := range []string{"refresh", "send", "network", "redirect"} {
		t.Run(failAt, func(t *testing.T) {
			s := testSender(t)
			calls := 0
			s.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if failAt == "network" {
					return nil, errors.New("private-token recipient@example.com")
				}
				if r.URL.String() != gmailSendURL && failAt != "refresh" {
					return response(200, `{"access_token":"test-access","token_type":"Bearer","expires_in":3600}`), nil
				}
				status := 400
				if failAt == "redirect" {
					status = 302
				}
				result := response(status, "private-token recipient@example.com")
				if status == 302 {
					result.Header.Set("Location", "https://untrusted.example/")
				}
				return result, nil
			})
			err := s.Send(context.Background(), testMessage())
			if err != ErrDelivery {
				t.Fatal("unsafe or missing delivery error")
			}
			if calls > 2 {
				t.Fatal("automatic retry or redirect occurred")
			}
		})
	}
}
func TestInvalidMessageNeverCallsProvider(t *testing.T) {
	s := testSender(t)
	s.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid message reached provider")
		return nil, nil
	})
	for _, m := range []Message{
		{To: "victim@example.com\r\nBcc: other@example.com", Purpose: VerifyEmail, Code: testMessage().Code},
		{To: "Display <recipient@example.com>", Purpose: VerifyEmail, Code: testMessage().Code},
		{To: "recipient@example.com", Purpose: VerifyEmail, Code: "invalid"},
		{To: "recipient@example.com", Purpose: "arbitrary", Code: testMessage().Code},
	} {
		if err := s.Send(context.Background(), m); err != ErrMessage {
			t.Fatal("invalid message accepted")
		}
	}
}
func TestCancellationWhileWaitingForRefresh(t *testing.T) {
	s := testSender(t)
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.accessToken(ctx); err != ErrDelivery {
		t.Fatal("cancellation ignored")
	}
	<-s.gate
}
func TestDevelopmentCannotBeEnabledInProduction(t *testing.T) {
	for _, c := range []struct {
		dev          bool
		origin, addr string
	}{
		{false, "https://localhost:8443", "127.0.0.1:1025"},
		{true, "https://public.example", "127.0.0.1:1025"},
		{true, "http://localhost:8080", "smtp.example.com:1025"},
		{true, "http://localhost:8080", "127.0.0.1:0"},
	} {
		if _, err := NewDevMailSender(c.dev, c.origin, c.addr); err == nil {
			t.Fatal("unsafe development mail configuration")
		}
	}
	t.Setenv("AUTH_MAIL_MODE", "development")
	t.Setenv("AUTH_DEV_SMTP_ADDR", "127.0.0.1:1025")
	if _, err := FromEnvironment(false, "https://vault.example"); err == nil {
		t.Fatal("production accepted local mail")
	}
	t.Setenv("AUTH_MAIL_MODE", "unknown")
	if _, err := FromEnvironment(true, "http://localhost:8080"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
func TestDevMailDelivery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- ""
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "220 localhost test mail\r\n")
		reader := bufio.NewReader(conn)
		var body strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- body.String()
				return
			}
			if strings.HasPrefix(line, "DATA") {
				_, _ = io.WriteString(conn, "354 send data\r\n")
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						done <- ""
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
			}
			_, _ = io.WriteString(conn, "250 OK\r\n")
		}
	}()
	s, err := NewDevMailSender(true, "http://127.0.0.1:8080", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	message := testMessage()
	message.Purpose = ResetPassword
	if err = s.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-done:
		if !strings.Contains(body, message.Code) || !strings.Contains(body, "Reset your Full Stack File Vault password") {
			t.Fatal("missing reset mail")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("SMTP capture timed out")
	}
}
func TestGmailConfigurationSecrets(t *testing.T) {
	t.Setenv("AUTH_MAIL_MODE", "gmail")
	t.Setenv("GMAIL_SENDER_CLIENT_ID", "test-id")
	t.Setenv("GMAIL_SENDER_CLIENT_SECRET", "test-secret")
	t.Setenv("GMAIL_SENDER_REFRESH_TOKEN", "test-refresh")
	t.Setenv("AUTH_MAIL_FROM", "sender@example.com")
	for _, key := range []string{"GMAIL_SENDER_CLIENT_ID_FILE", "GMAIL_SENDER_CLIENT_SECRET_FILE", "GMAIL_SENDER_REFRESH_TOKEN_FILE"} {
		t.Setenv(key, "")
	}
	if _, err := FromEnvironment(false, "https://vault.example"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GMAIL_SENDER_REFRESH_TOKEN", "")
	if _, err := FromEnvironment(false, "https://vault.example"); err == nil {
		t.Fatal("missing credential accepted")
	}
	t.Setenv("AUTH_MAIL_MODE", "disabled")
	if s, err := FromEnvironment(false, "https://vault.example"); err != nil || s != nil {
		t.Fatal("disabled sender not disabled")
	}
}
