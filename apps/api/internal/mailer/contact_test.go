package mailer

import (
	"encoding/base64"
	"io"
	"net/mail"
	"strings"
	"testing"
)

func TestContactMessage(t *testing.T) {
	m := Message{To: AdministratorInbox, Purpose: ContactAdministrator, Subject: "Help signing in", Body: "Reply to visitor@example.test\nUnicode: café"}
	raw, err := encodeMessage("sender@example.test", m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("To") != AdministratorInbox || parsed.Header.Get("Subject") != "Full Stack File Vault support request" {
		t.Fatal("unexpected headers")
	}
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
	if err != nil || !strings.Contains(string(body), m.Body) {
		t.Fatal("body encoding")
	}
	m.To = "attacker@example.test"
	if _, err = encodeMessage("sender@example.test", m); err != ErrMessage {
		t.Fatal("recipient override")
	}
	for _, subject := range []string{"", strings.Repeat("x", 121), "Hello\r\nBcc: attacker@example.test"} {
		m.To = AdministratorInbox
		m.Subject = subject
		if _, err = encodeMessage("sender@example.test", m); err != ErrMessage {
			t.Fatal("invalid subject accepted")
		}
	}
	if ValidContact("Help", strings.Repeat("x", 4001)) || ValidContact("Help", "\x00") {
		t.Fatal("invalid body accepted")
	}
}
