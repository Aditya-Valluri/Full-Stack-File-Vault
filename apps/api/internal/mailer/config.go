package mailer

import (
	"errors"
	"os"

	"full-stack-file-vault.local/api/internal/secret"
)

// FromEnvironment returns nil when delivery is disabled. Incomplete/invalid
// explicit configuration is an error, never a fallback to development delivery.
func FromEnvironment(development bool, origin string) (MailSender, error) {
	switch os.Getenv("AUTH_MAIL_MODE") {
	case "", "disabled":
		return nil, nil
	case "development":
		return NewDevMailSender(development, origin, os.Getenv("AUTH_DEV_SMTP_ADDR"))
	case "gmail":
		id, err := secret.Read("GMAIL_SENDER_CLIENT_ID")
		if err != nil {
			return nil, err
		}
		clientSecret, err := secret.Read("GMAIL_SENDER_CLIENT_SECRET")
		if err != nil {
			return nil, err
		}
		refresh, err := secret.Read("GMAIL_SENDER_REFRESH_TOKEN")
		if err != nil {
			return nil, err
		}
		return NewGmailMailSender(GmailConfig{ClientID: id, ClientSecret: clientSecret, RefreshToken: refresh, From: os.Getenv("AUTH_MAIL_FROM")})
	default:
		return nil, errors.New("unsupported AUTH_MAIL_MODE")
	}
}
