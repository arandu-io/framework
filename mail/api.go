// The two provider transports, answered by
// github.com/arandu-io/hesape/mail/transport -- the same two names there, with
// a third, Postmark, and a fourth that is not a provider, Failover. Neither of
// those two is reachable through this package: a bridge carries the old surface
// across and does not grow one.
//
// Shells rather than aliases, for the reason transport.go gives: hesape's Send
// answers a receipt as well as an error.

package mail

import (
	"context"
	"net/http"
	"time"

	"github.com/arandu-io/hesape/mail/transport"
)

// Resend sends through resend.com.
//
// It is the default recommendation for an application that has outgrown the log
// transport: a domain, a DNS record and an API key, and no server to run.
//
// The fields are already the same as transport.Resend. What keeps this a
// separate type is Send: it answers an error here and a receipt as well there,
// so an alias would no longer satisfy the Transport interface.
type Resend struct {
	// Key is the API key, `re_...`. It comes from the environment and never from
	// a literal -- a key in source is a key in every clone of the repository.
	Key string

	// Endpoint overrides the API, for a test. Empty is resend.com.
	Endpoint string

	// Timeout bounds the request. Without one a hung provider holds the request
	// that triggered it for as long as the provider likes.
	Timeout time.Duration

	// Client is the HTTP client. Empty builds one with Timeout.
	Client *http.Client
}

func (t Resend) hesape() transport.Resend {
	return transport.Resend{
		Key:      t.Key,
		Endpoint: t.Endpoint,
		Timeout:  t.Timeout,
		Client:   t.Client,
	}
}

// Name identifies the transport in a log line.
func (t Resend) Name() string { return t.hesape().Name() }

// Send posts the message.
func (t Resend) Send(ctx context.Context, m Message) error {
	_, err := t.hesape().Send(ctx, m.hesape())
	return err
}

// SendGrid sends through sendgrid.com.
//
// The second provider rather than the only one, because a transport with one
// implementation is an interface nobody has proved is an interface.
//
// The fields are already the same as transport.SendGrid, and Send is the one
// difference: an error here, a receipt as well there.
type SendGrid struct {
	// Key is the API key, `SG....`.
	Key string

	// Endpoint overrides the API, for a test. Empty is sendgrid.com.
	Endpoint string

	// Timeout bounds the request.
	Timeout time.Duration

	// Client is the HTTP client. Empty builds one with Timeout.
	Client *http.Client
}

func (t SendGrid) hesape() transport.SendGrid {
	return transport.SendGrid{
		Key:      t.Key,
		Endpoint: t.Endpoint,
		Timeout:  t.Timeout,
		Client:   t.Client,
	}
}

// Name identifies the transport in a log line.
func (t SendGrid) Name() string { return t.hesape().Name() }

// Send posts the message.
func (t SendGrid) Send(ctx context.Context, m Message) error {
	_, err := t.hesape().Send(ctx, m.hesape())
	return err
}

var (
	_ Transport = Resend{}
	_ Transport = SendGrid{}
)
