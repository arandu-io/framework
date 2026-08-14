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
	"time"

	"github.com/arandu-io/hesape/mail/transport"
)

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
