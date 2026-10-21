// The three transports that need no provider, answered by
// github.com/arandu-io/hesape/mail/transport -- the same three names there,
// which is why the fields below are the fields there.
//
// None of them is an alias, for one reason that applies to all five transports
// in this package: hesape's Send answers (mail.SentMessage, error) where the
// Transport interface here answers error. Each is a shell that builds the hesape
// transport, calls it, and drops the receipt.

package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/arandu-io/hesape/mail/transport"
)

// SMTP sends over SMTP: over implicit TLS when Encryption is "tls", and
// otherwise with STARTTLS when the server offers it.
//
// # Why this one is declared and not aliased
//
// The four fields below are already the same four as transport.SMTP, in the
// same order and the same types. What keeps this a separate type is the one
// method: Send answers an error here and (mail.SentMessage, error) there, and
// it takes this package's Message rather than that one's. Aliasing the name
// would swap a Transport for something the Transport interface does not
// accept.
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

	// Encryption is how the connection is protected, as MAIL_URL names it.
	// "tls" is implicit TLS from the first byte, what smtps:// and port 465
	// mean: the handshake comes before any SMTP is spoken, and a server that
	// does not complete it is sent nothing. "starttls", what smtp:// and port
	// 587 mean, and empty both upgrade a plain connection when the server
	// offers STARTTLS; a password is never sent over one that was not
	// upgraded, except to a relay on this machine.
	Encryption string

	// TLSConfig verifies the server over implicit TLS. Nil checks the
	// certificate against the system roots for Host; a test, or a relay with a
	// private authority, passes its own.
	TLSConfig *tls.Config
}

func (t SMTP) hesape() transport.SMTP {
	return transport.SMTP{
		Host:     t.Host,
		Port:     t.Port,
		Username: t.Username,
		Password: t.Password,
		Timeout:  t.Timeout,
	}
}

// Name identifies the transport in a log line.
func (t SMTP) Name() string { return t.hesape().Name() }

// Send delivers the message.
func (t SMTP) Send(ctx context.Context, m Message) error {
	switch t.Encryption {
	case "", encryptionStartTLS:
	case encryptionTLS:
		return t.sendImplicitTLS(ctx, m.hesape())
	default:
		// A misspelt "tsl" read as anything would be the failure the field
		// exists to rule out.
		return fmt.Errorf("mail: SMTP Encryption is %q; expected %q or %q", t.Encryption, encryptionTLS, encryptionStartTLS)
	}
	_, err := t.hesape().Send(ctx, m.hesape())
	return err
}

// Log writes the message to the log instead of sending it.
//
// It is the development default, and what makes `aru dev` work with nothing
// installed. The whole body is logged, because the reason to read it is to
// follow the link inside.
//
// It writes wherever the context is logging, as it always did. What changed is
// which package the context is asked: it is hesape/log now rather than
// framework/observability, and framework/observability is a bridge over that
// same package, so a request logger installed by either is the one found here.
//
// It is a shell over transport.Log rather than an alias, for the reason that
// applies to all five transports here: hesape's Send answers a receipt as well
// as an error, and this one has to satisfy the Transport interface that does
// not.
type Log struct{}

// Name identifies the transport in a log line.
func (Log) Name() string { return transport.Log{}.Name() }

// Send logs the message.
func (Log) Send(ctx context.Context, m Message) error {
	_, err := transport.Log{}.Send(ctx, m.hesape())
	return err
}

// Array keeps what was sent, for a test to read.
//
// It is safe for concurrent use, because a test that sends from two goroutines
// and reads from a third is a test that would otherwise fail under -race for a
// reason that has nothing to do with what it is proving. The lock and the slice
// are hesape's: this type holds no storage of its own.
//
// Its three readers were renamed on the way over -- Sent is Messages there and
// Reset is Flush -- so the three below are the old names reaching the new ones.
// That rename is one of the two reasons this is not an alias; the other is
// Send, which answers an error here and a receipt as well there.
type Array struct {
	inner transport.Array
}

// Name identifies the transport in a log line.
func (a *Array) Name() string { return a.inner.Name() }

// Send records the message.
func (a *Array) Send(ctx context.Context, m Message) error {
	_, err := a.inner.Send(ctx, m.hesape())
	return err
}

// Sent is everything sent so far, oldest first.
func (a *Array) Sent() []Message {
	kept := a.inner.Messages()
	out := make([]Message, 0, len(kept))
	for _, m := range kept {
		out = append(out, messageFrom(m))
	}
	return out
}

// Last is the most recent message, and whether there was one.
func (a *Array) Last() (Message, bool) {
	m, ok := a.inner.Last()
	if !ok {
		return Message{}, false
	}
	return messageFrom(m), true
}

// Reset forgets everything. A test that shares a transport between cases calls
// it, and one that does not share it does not need to.
func (a *Array) Reset() { a.inner.Flush() }

var (
	_ Transport = SMTP{}
	_ Transport = Log{}
	_ Transport = (*Array)(nil)
)
