package mailer

import (
	"context"
	"errors"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"time"
)

// DevMailSender delivers only to a loopback SMTP capture tool, never a real
// external SMTP service. Its constructor rejects production and public origins.
type DevMailSender struct{ address, from string }

func NewDevMailSender(development bool, origin, address string) (*DevMailSender, error) {
	invalid := errors.New("development mail requires loopback development configuration")
	u, err := url.Parse(origin)
	if err != nil || !development || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, invalid
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, invalid
	}
	smtpHost, port, err := net.SplitHostPort(address)
	smtpIP := net.ParseIP(smtpHost)
	n, parseErr := strconv.Atoi(port)
	if err != nil || smtpIP == nil || !smtpIP.IsLoopback() || parseErr != nil || n < 1 || n > 65535 {
		return nil, invalid
	}
	return &DevMailSender{address: address, from: "no-reply@file-vault.test"}, nil
}
func (s *DevMailSender) Send(ctx context.Context, m Message) error {
	message, err := encodeMessage(s.from, m)
	if err != nil {
		return err
	}
	defer clear(message)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return ErrDelivery
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return ErrDelivery
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		return ErrDelivery
	}
	defer client.Close()
	if err = client.Mail(s.from); err != nil {
		return ErrDelivery
	}
	if err = client.Rcpt(m.To); err != nil {
		return ErrDelivery
	}
	writer, err := client.Data()
	if err != nil {
		return ErrDelivery
	}
	_, err = writer.Write(message)
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		return ErrDelivery
	}
	return nil
}
