// SMTP over implicit TLS.

package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"

	hmail "github.com/arandu-io/hesape/mail"
)

// The two values Encryption takes, as MAIL_URL writes them.
const (
	encryptionTLS      = "tls"
	encryptionStartTLS = "starttls"
)

// sendImplicitTLS delivers m over a connection that is TLS from the first
// byte: the handshake completes, against TLSConfig or the system roots for
// Host, before the server's greeting is read.
func (t SMTP) sendImplicitTLS(ctx context.Context, m hmail.Message) error {
	// Rendered before dialling: a message that cannot be rendered must not
	// reach the server, which RCPT TO would already have told who it was for.
	rendered, err := hmail.Render(m)
	if err != nil {
		return err
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	addr := net.JoinHostPort(t.Host, t.Port)
	raw, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		// Unreachable is the same kind of event as a provider answering 502:
		// nothing about the message was refused, so a later try may work.
		return hmail.Retryable(fmt.Errorf("mail: dialing %s: %w", addr, err))
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(timeout))

	conn := tls.Client(raw, t.tlsConfig())
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("mail: tls with %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		return hmail.Retryable(fmt.Errorf("mail: %s: %w", addr, err))
	}
	defer c.Close()

	if t.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", t.Username, t.Password, t.Host)); err != nil {
			return fmt.Errorf("mail: authenticating: %w", err)
		}
	}
	if err := c.Mail(m.From.Address); err != nil {
		return fmt.Errorf("mail: from %s: %w", m.From.Address, err)
	}
	for _, a := range append(append(append([]hmail.Address{}, m.To...), m.CC...), m.BCC...) {
		if err := c.Rcpt(a.Address); err != nil {
			return fmt.Errorf("mail: to %s: %w", a.Address, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write([]byte(rendered)); err != nil {
		return fmt.Errorf("mail: writing the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: closing the message: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("mail: closing the session: %w", err)
	}
	return nil
}

// tlsConfig is TLSConfig, or the system roots for Host. A config without a
// ServerName is given Host, because a certificate is checked against a name.
func (t SMTP) tlsConfig() *tls.Config {
	if t.TLSConfig == nil {
		return &tls.Config{ServerName: t.Host, MinVersion: tls.VersionTLS12}
	}
	cfg := t.TLSConfig.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = t.Host
	}
	return cfg
}
