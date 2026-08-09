package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/observability"
)

// The transports that ship in the core, and why these five.
//
// SMTP is the one every provider speaks, and it needs no dependency: net/smtp
// is in the standard library. Whatever somebody buys, it accepts SMTP, so an
// application is never blocked waiting for an adapter to exist.
//
// Log is what development uses. Laravel ships the same thing, and for the same
// reason: an example that needs a mail server is an example nobody runs.
//
// Array is what tests use. It keeps what was sent so a test can read it, which
// is the difference between proving an e-mail was sent and proving nothing.
//
// Resend and SendGrid are in api.go, in this package, because neither needs a
// dependency: both are one POST with a JSON body, which net/http already does.
// RULE 11 sends an adapter to a submodule when it drags an SDK in behind it, and
// these two drag nothing. Amazon SES is the one that would -- it needs request
// signing -- and it is deliberately absent. See ADR 0031.

// SMTP sends over SMTP, with STARTTLS.
type SMTP struct {
	// Host and Port are the server. 587 is submission with STARTTLS, which is
	// what a provider gives you; 25 is server-to-server and is usually blocked.
	Host string
	Port string

	// Username and Password authenticate. Both empty sends unauthenticated,
	// which is right for a local relay and wrong for anything reachable.
	Username string
	Password string

	// Timeout bounds the whole exchange. Without one a hung server holds the
	// request that triggered it until the client gives up -- and net/smtp has no
	// deadline of its own.
	Timeout time.Duration
}

// Name identifies the transport in a log line.
func (SMTP) Name() string { return "smtp" }

// Send delivers the message.
func (t SMTP) Send(ctx context.Context, m Message) error {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	addr := net.JoinHostPort(t.Host, t.Port)
	dialer := &net.Dialer{Timeout: timeout}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: dialing %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	c, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		return fmt.Errorf("mail: %s: %w", addr, err)
	}
	defer c.Close()

	// STARTTLS when the server offers it, and that is not optional when there
	// are credentials: PlainAuth refuses to send a password over a connection
	// that is not encrypted, and it is right to.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: t.Host}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if t.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", t.Username, t.Password, t.Host)); err != nil {
			return fmt.Errorf("mail: authenticating: %w", err)
		}
	}

	if err := c.Mail(m.From.Email); err != nil {
		return fmt.Errorf("mail: from %s: %w", m.From.Email, err)
	}
	for _, a := range append(append(append([]Address{}, m.To...), m.CC...), m.BCC...) {
		if err := c.Rcpt(a.Email); err != nil {
			return fmt.Errorf("mail: to %s: %w", a.Email, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write([]byte(Render(m))); err != nil {
		return fmt.Errorf("mail: writing the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: closing the message: %w", err)
	}
	return c.Quit()
}

// Log writes the message to the log instead of sending it.
//
// It is the development default, and what makes `aru dev` work with nothing
// installed. The whole body is logged, because the reason to read it is to
// follow the link inside.
type Log struct{}

// Name identifies the transport in a log line.
func (Log) Name() string { return "log" }

// Send logs the message.
func (Log) Send(ctx context.Context, m Message) error {
	to := make([]string, 0, len(m.To))
	for _, a := range m.To {
		to = append(to, a.Email)
	}

	observability.Log(ctx).Info("mail: this transport logs instead of sending",
		"to", strings.Join(to, ", "),
		"subject", m.Subject,
		"body", firstNonEmpty(m.Text, m.HTML))
	return nil
}

// Array keeps what was sent, for a test to read.
//
// It is safe for concurrent use, because a test that sends from two goroutines
// and reads from a third is a test that would otherwise fail under -race for a
// reason that has nothing to do with what it is proving.
type Array struct {
	mu   sync.Mutex
	sent []Message
}

// Name identifies the transport in a log line.
func (*Array) Name() string { return "array" }

// Send records the message.
func (a *Array) Send(_ context.Context, m Message) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, m)
	return nil
}

// Sent is everything sent so far, oldest first.
func (a *Array) Sent() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Message(nil), a.sent...)
}

// Last is the most recent message, and whether there was one.
func (a *Array) Last() (Message, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sent) == 0 {
		return Message{}, false
	}
	return a.sent[len(a.sent)-1], true
}

// Reset forgets everything. A test that shares a transport between cases calls
// it, and one that does not share it does not need to.
func (a *Array) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
