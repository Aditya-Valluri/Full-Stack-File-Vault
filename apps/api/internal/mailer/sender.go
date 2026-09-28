// Package mailer sends authentication messages without exposing their credentials.
package mailer

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrDelivery = errors.New("authentication email delivery unavailable")
var ErrMessage = errors.New("invalid authentication email")

type Purpose string

const (
	VerifyEmail          Purpose = "verify-email"
	ResetPassword        Purpose = "reset-password"
	ContactAdministrator Purpose = "contact-administrator"
)

// Message is transient sensitive data. Never log, serialize into API responses,
// or persist its Code; authentication stores only a hash of that credential.
type Message struct {
	To      string
	Purpose Purpose
	Code    string
	Subject string
	Body    string
}
type MailSender interface {
	Send(context.Context, Message) error
}

// ValidContact bounds UTF-8 bytes and rejects header/control-character injection.
func ValidContact(subject, body string) bool {
	if len(subject) < 1 || len(subject) > 120 || len(body) < 1 || len(body) > 4000 ||
		strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "" || !utf8.ValidString(subject) || !utf8.ValidString(body) {
		return false
	}
	for _, c := range subject {
		if unicode.IsControl(c) {
			return false
		}
	}
	for _, c := range body {
		if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}
	return true
}
func validAddress(address string) bool {
	if len(address) > 254 || strings.TrimSpace(address) != address || !strings.Contains(address, "@") {
		return false
	}
	for _, c := range address {
		if c < 33 || c > 126 {
			return false
		}
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Name == "" && parsed.Address == address
}
func encodeMessage(from string, m Message) ([]byte, error) {
	if !validAddress(from) || !validAddress(m.To) {
		return nil, ErrMessage
	}
	if m.Purpose == ContactAdministrator {
		if m.To != AdministratorInbox || !ValidContact(m.Subject, m.Body) {
			return nil, ErrMessage
		}
		// User text stays in a base64 plain-text body, never in mail headers.
		body := "Unverified visitor support request. Treat the contents as untrusted.\n\nSubject: " + m.Subject + "\n\n" + m.Body
		return []byte("From: Full Stack File Vault <" + from + ">\r\nTo: " + AdministratorInbox + "\r\nSubject: Full Stack File Vault support request\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + encodeContactBody(body)), nil
	}
	if m.Purpose == VerifyEmail || m.Purpose == ResetPassword {
		if len(m.Code) != 7 {
			return nil, ErrMessage
		}
		for _, c := range m.Code {
			if c < '0' || c > '9' {
				return nil, ErrMessage
			}
		}
	} else {
		return nil, ErrMessage
	}
	var subject, intro string
	switch m.Purpose {
	case VerifyEmail:
		subject = "Verify your Full Stack File Vault email"
		intro = "Paste this verification code into Full Stack File Vault to continue creating your account."
	case ResetPassword:
		subject = "Reset your Full Stack File Vault password"
		intro = "Paste this reset code into Full Stack File Vault to choose a new password."
	default:
		return nil, ErrMessage
	}
	// Fixed headers, validated single mailboxes, no HTML, links, tracking, or attachments.
	text := "From: Full Stack File Vault <" + from + ">\r\nTo: " + m.To + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n" + intro + "\r\n\r\n" + m.Code + "\r\n\r\nThis code is valid for a limited time and can be used only once.\r\nIf you did not request this message, ignore it. Never share this code.\r\n"
	return []byte(text), nil
}
