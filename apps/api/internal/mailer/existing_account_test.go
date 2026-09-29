package mailer

import (
	"strings"
	"testing"
)

func TestExistingAccountInstructionsContainNoCode(t *testing.T) {
	m := Message{To: "existing@example.test", Purpose: ExistingAccount}
	raw, err := encodeMessage("sender@example.test", m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Sign in or Reset password") || strings.Contains(string(raw), "0123456") {
		t.Fatal("incorrect instructions")
	}
	m.Code = "0123456"
	if _, err = encodeMessage("sender@example.test", m); err != ErrMessage {
		t.Fatal("instruction mail accepted a code")
	}
}
