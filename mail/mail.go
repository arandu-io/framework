// The message vocabulary, answered by github.com/arandu-io/hesape/mail.
//
// The types here are declared rather than aliased, because hesape spells a
// mailbox Address{Address, Name} and this package spells it Address{Email,
// Name}: every type that carries an address had to follow. What they hold is a
// translation into the hesape type of the same name, and nothing else.

package mail

import (
	"context"

	hmail "github.com/arandu-io/hesape/mail"
)

// ErrNoRecipient is returned by Send when nobody was addressed. It is an error
// rather than a silent no-op: a message with no recipient is a message somebody
// meant to send.
var ErrNoRecipient = hmail.ErrNoRecipient

// ErrRetryable marks a failure worth trying again.
//
// A 429 or a 5xx from a provider is not the same event as a rejected address,
// and treating them alike is how a verification e-mail is silently lost during a
// rate limit. A job that sends checks for this and reschedules; a request that
// sends inline reports it and moves on.
//
// The alias is what keeps it one type: an errors.As against this name matches
// the value a hesape transport wrapped.
type ErrRetryable = hmail.ErrRetryable

// Address is one mailbox, with the display name that goes in front of it.
//
// Declared here rather than aliased: the first field is called Address in
// hesape, and every call site in the collection writes mail.Address{Email: ...}.
type Address struct {
	// Email is the address itself, and the only required half.
	Email string
	// Name is what a client shows instead of the address. Empty is fine.
	Name string
}

// String renders the address the way a header carries it.
func (a Address) String() string { return a.hesape().String() }

// Valid reports whether the address parses. It is checked before a transport is
// asked to do anything, so a typo fails at the call rather than as a bounce
// three minutes later.
func (a Address) Valid() bool { return a.hesape().Valid() }

// hesape is the same mailbox under the name the collection now uses.
func (a Address) hesape() hmail.Address {
	return hmail.Address{Address: a.Email, Name: a.Name}
}

func addressFrom(a hmail.Address) Address {
	return Address{Email: a.Address, Name: a.Name}
}

func addressesTo(list []Address) []hmail.Address {
	if len(list) == 0 {
		return nil
	}
	out := make([]hmail.Address, 0, len(list))
	for _, a := range list {
		out = append(out, a.hesape())
	}
	return out
}

func addressesFrom(list []hmail.Address) []Address {
	if len(list) == 0 {
		return nil
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, addressFrom(a))
	}
	return out
}

// Envelope is who a message is from, who it is to, and what it says it is.
type Envelope struct {
	From    Address
	To      []Address
	CC      []Address
	BCC     []Address
	ReplyTo []Address

	Subject string

	// Tags and Metadata are carried by the transports that support them and
	// dropped by the ones that do not. They are how a provider's dashboard
	// groups "password resets" apart from "invoices".
	Tags     []string
	Metadata map[string]string
}

func (e Envelope) hesape() hmail.Envelope {
	return hmail.Envelope{
		From:     e.From.hesape(),
		To:       addressesTo(e.To),
		CC:       addressesTo(e.CC),
		BCC:      addressesTo(e.BCC),
		ReplyTo:  addressesTo(e.ReplyTo),
		Subject:  e.Subject,
		Tags:     e.Tags,
		Metadata: e.Metadata,
	}
}

// envelopeFrom drops hesape's Using, which this package never had a field for
// and which has already run by the time a transport sees the message.
func envelopeFrom(e hmail.Envelope) Envelope {
	return Envelope{
		From:     addressFrom(e.From),
		To:       addressesFrom(e.To),
		CC:       addressesFrom(e.CC),
		BCC:      addressesFrom(e.BCC),
		ReplyTo:  addressesFrom(e.ReplyTo),
		Subject:  e.Subject,
		Tags:     e.Tags,
		Metadata: e.Metadata,
	}
}

// Content is what the body is made of.
//
// A view name and its data, rather than a string: the message is drawn by the
// same view layer as a page, so a field that does not exist is a compile error
// and interpolation is escaped by construction.
type Content struct {
	// View is the HTML part, by the name the view is registered under.
	View string

	// TextView is the plain-text part, also by view name. It is Text in hesape,
	// where it means the same thing.
	//
	// A message with no text part is filed as spam more often, and every client
	// that cannot render HTML shows nothing at all.
	TextView string

	// Text is the plain-text part as a literal, for a message short enough that
	// a view would be ceremony. TextView wins when both are set.
	//
	// hesape has no field for a literal text body, so the bridge applies this
	// one to the built message rather than to the content it hands over.
	Text string

	// Data is what both parts render from. It is With in hesape.
	Data any
}

// Mailable is anything that knows how to describe itself as a message.
type Mailable interface {
	Envelope() Envelope
	Content() Content
}

// Message is what a Transport receives: an envelope and the two rendered parts.
//
// The transport never sees a view name or a Mailable. Rendering happens once, in
// the Mailer, so a transport cannot render differently from another one.
//
// hesape's Message carries attachments, headers, embedded parts and a priority
// as well. None of them has ever been reachable through this package, so a
// message crossing this boundary loses nothing it was carrying.
type Message struct {
	Envelope
	HTML string
	Text string
}

func (m Message) hesape() hmail.Message {
	return hmail.Message{Envelope: m.Envelope.hesape(), HTML: m.HTML, Text: m.Text}
}

func messageFrom(m hmail.Message) Message {
	return Message{Envelope: envelopeFrom(m.Envelope), HTML: m.HTML, Text: m.Text}
}

// Transport delivers a rendered message.
//
// One method, so writing one is small: an adapter for a provider is a POST and
// an error, and everything above it -- addressing, rendering, validation -- has
// already happened.
//
// Declared here with the old shape, and not aliased: hesape's Send answers a
// receipt as well as an error, and an alias would compile in this module while
// silently refusing every Transport written in another one.
type Transport interface {
	Send(ctx context.Context, m Message) error
	// Name is what appears in a log line and on the debug console.
	Name() string
}

// Renderer draws the view a Content names.
//
// An interface here rather than the view package directly, because mail is
// imported by the modules that send and importing the view package from all of
// them would put the whole view registry behind every one. framework/view
// satisfies it.
type Renderer = hmail.Renderer

// transportAdapter carries a Transport written against this package across to
// hesape, which asks for a receipt the old interface has no way to produce. The
// receipt names the transport and nothing else, which is what the transports
// with no identifier of their own answer there too.
type transportAdapter struct{ inner Transport }

func (t transportAdapter) Name() string { return t.inner.Name() }

func (t transportAdapter) Send(ctx context.Context, m hmail.Message) (hmail.SentMessage, error) {
	if err := t.inner.Send(ctx, messageFrom(m)); err != nil {
		return hmail.SentMessage{}, err
	}
	return hmail.SentMessage{Transport: t.inner.Name()}, nil
}

// mailableAdapter carries a Mailable written against this package across to
// hesape.
//
// The literal Content.Text is the one field with no counterpart on the other
// side, so it arrives as a callback on the envelope: hesape runs those after it
// has rendered, which is the point in Build where this package used to assign
// the same field.
type mailableAdapter struct{ inner Mailable }

func (a mailableAdapter) Envelope() hmail.Envelope {
	env := a.inner.Envelope().hesape()
	if content := a.inner.Content(); content.TextView == "" && content.Text != "" {
		text := content.Text
		env.Using = append(env.Using, func(m *hmail.Message) { m.Text = text })
	}
	return env
}

func (a mailableAdapter) Content() hmail.Content {
	c := a.inner.Content()
	return hmail.Content{View: c.View, Text: c.TextView, With: c.Data}
}

var _ hmail.Mailable = mailableAdapter{}
var _ hmail.Transport = transportAdapter{}
