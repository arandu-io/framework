// The Mailer and the pending message, answered by
// github.com/arandu-io/hesape/mail -- Mailer there too, and PendingMail rather
// than Pending.
//
// Both are envelopes rather than aliases: hesape's constructor takes a mailer
// name and an event dispatcher this package has no argument for, its To takes
// one polymorphic argument where this one takes strings, and its Send answers a
// receipt as well as an error.

package mail

import (
	"context"
	"errors"
	"strings"

	hmail "github.com/arandu-io/hesape/mail"
)

// mailerName is what hesape calls a Mailer that was built without a name. It is
// Illuminate's default connection name, and it reaches nothing this package
// exposes: only an event listener and the manager read it, and neither is
// wired here.
const mailerName = "default"

// errNoSubject is what a message with no subject has always failed with.
//
// It is checked in the bridge rather than left to hesape, and that is the one
// judgement in this file. hesape does not refuse a subjectless message: it
// derives one from the mailable's type name, the way Illuminate does, so
// OrderShipped goes out as "Order Shipped". The type it would see here is the
// adapter that carries a Mailable across, so the derived subject would be the
// name of a bridge -- a worse outcome than either design intends.
var errNoSubject = errors.New("mail: the envelope has no subject")

// Mailer sends a Mailable through a Transport.
type Mailer struct {
	inner *hmail.Mailer

	// transport is the one handed to New, kept so that Transport answers the
	// same value it was given: a test reads what was sent by asserting on that
	// pointer, and an adapter around it would break the assertion.
	transport Transport
}

// New returns a Mailer.
func New(t Transport, r Renderer, from Address) *Mailer {
	inner := hmail.New(mailerName, r, transportAdapter{inner: t}, nil)
	inner.AlwaysFrom(from.Email, from.Name)
	return &Mailer{inner: inner, transport: t}
}

// To starts a message to one or more addresses.
//
// It returns a pending message rather than sending, so cc and bcc chain in the
// order they are read.
func (m *Mailer) To(addresses ...string) *Pending {
	return &Pending{inner: m.inner.To(addresses)}
}

// ToAddress is To for a recipient whose display name is known.
func (m *Mailer) ToAddress(addresses ...Address) *Pending {
	return &Pending{inner: m.inner.To(addressesTo(addresses))}
}

// Transport is which one is wired, for a health check or a log line.
func (m *Mailer) Transport() Transport { return m.transport }

// Pending is a message being addressed.
type Pending struct {
	inner *hmail.PendingMail
}

// CC adds recipients.
func (p *Pending) CC(addresses ...string) *Pending {
	p.inner.CC(addresses)
	return p
}

// BCC adds recipients nobody else sees.
func (p *Pending) BCC(addresses ...string) *Pending {
	p.inner.BCC(addresses)
	return p
}

// Send renders the mailable and hands it to the transport.
//
// It is synchronous. Sending on the queue is a job that calls this, and that is
// deliberate: a call that sometimes blocks for two seconds and sometimes does
// not, decided by an interface the mailable implements somewhere else, is a call
// nobody can reason about from the line they are reading.
func (p *Pending) Send(ctx context.Context, mailable Mailable) error {
	if strings.TrimSpace(mailable.Envelope().Subject) == "" {
		return errNoSubject
	}
	_, err := p.inner.Send(ctx, mailableAdapter{inner: mailable})
	return err
}
