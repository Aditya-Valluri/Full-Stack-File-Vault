package auth

import (
	"context"
	"errors"
	"full-stack-file-vault.local/api/internal/mailer"
	"time"
)

var ErrContactInvalid = errors.New("invalid contact request")

type contactContextKey struct{}
type contactCapability func(context.Context, string, string) error

// ContactAdministrator is available to the anonymous sign-in page only.
func (s *SessionStore) ContactAdministrator(ctx context.Context, token, csrf, peer, subject, message string) error {
	if s.mail == nil {
		return ErrEmailDisabled
	}
	if err := s.checkAnonymous(ctx, token, csrf); err != nil {
		return err
	}
	if !mailer.ValidContact(subject, message) {
		return ErrContactInvalid
	}
	// A fixed identifier caps all support delivery at five messages per 15 minutes,
	// across browsers and instances, in addition to existing peer/global budgets.
	if err := s.reserveLoginAttempt(ctx, peer, "support-contact"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.mail.Send(ctx, mailer.Message{To: mailer.AdministratorInbox, Purpose: mailer.ContactAdministrator, Subject: subject, Body: message}); err != nil {
		return mailer.ErrDelivery
	}
	return nil
}
func ContactAdministratorFromContext(ctx context.Context, subject, message string) error {
	fn, ok := ctx.Value(contactContextKey{}).(contactCapability)
	if !ok {
		return ErrLoginForbidden
	}
	return fn(ctx, subject, message)
}
