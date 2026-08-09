// Package mail sends what an application has to say to somebody.
//
// The shape is Laravel's, because a developer coming from there should
// recognise it: a Mailable declares an Envelope and a Content, a Mailer sends
// it, and the transport behind the Mailer is configuration rather than a
// decision the calling code makes.
//
//	type WelcomeEmail struct{ Name, Link string }
//
//	func (m WelcomeEmail) Envelope() mail.Envelope {
//		return mail.Envelope{Subject: "Welcome"}
//	}
//
//	func (m WelcomeEmail) Content() mail.Content {
//		return mail.Content{View: "mail.welcome", Data: m}
//	}
//
//	mailer.To("you@example.com").Send(ctx, WelcomeEmail{Name: "Ada"})
//
// # What is deliberately absent
//
// No facade, no `Mail::` global, and no queue by default. Laravel queues a
// mailable when it implements ShouldQueue, which is an interface the class
// opts into at a distance; here sending on the queue is `jobs.Dispatch` with a
// job that sends, and the difference is visible at the call site.
//
// No markdown mailables. Laravel has them because Blade cannot easily produce
// both an HTML and a text part; kyse produces whatever the view produces, and a
// second templating language for e-mail is the second way to draw a page that
// RULE 9 refuses.
package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// ErrNoRecipient is returned by Send when nobody was addressed. It is an error
// rather than a silent no-op: a message with no recipient is a message somebody
// meant to send.
var ErrNoRecipient = errors.New("mail: no recipient")

// Address is one mailbox, with the display name that goes in front of it.
type Address struct {
	// Email is the address itself, and the only required half.
	Email string
	// Name is what a client shows instead of the address. Empty is fine.
	Name string
}

// String renders the address the way a header carries it.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// Valid reports whether the address parses. It is checked before a transport is
// asked to do anything, so a typo fails at the call rather than as a bounce
// three minutes later.
func (a Address) Valid() bool {
	if a.Email == "" {
		return false
	}
	_, err := mail.ParseAddress(a.Email)
	return err == nil
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

// Content is what the body is made of.
//
// A view name and its data, rather than a string: the message is drawn by the
// same view layer as a page, so a field that does not exist is a compile error
// and interpolation is escaped by construction.
type Content struct {
	// View is the HTML part, by the name the view is registered under.
	View string

	// TextView is the plain-text part, also by view name.
	//
	// It exists because Text alone did not do the job it was written for. A
	// project generated a mail/password-reset-text view, and nothing could ever
	// send it: the only way in was a Go string literal, so the view sat in the
	// tree looking wired while every message went out HTML-only. Found by audit.
	//
	// A message with no text part is filed as spam more often, and every client
	// that cannot render HTML shows nothing at all.
	TextView string

	// Text is the plain-text part as a literal, for a message short enough that
	// a view would be ceremony. TextView wins when both are set.
	Text string

	// Data is what both parts render from.
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
type Message struct {
	Envelope
	HTML string
	Text string
}

// Transport delivers a rendered message.
//
// One method, so writing one is small: an adapter for a provider is a POST and
// an error, and everything above it -- addressing, rendering, validation -- has
// already happened.
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
type Renderer interface {
	RenderToString(name string, data any) (string, error)
}

// Mailer sends a Mailable through a Transport.
type Mailer struct {
	transport Transport
	render    Renderer

	// from is the default sender, from configuration. An envelope that names
	// one wins; most do not, because the address an application sends from is a
	// property of the application rather than of the message.
	from Address
}

// New returns a Mailer.
func New(t Transport, r Renderer, from Address) *Mailer {
	return &Mailer{transport: t, render: r, from: from}
}

// To starts a message to one or more addresses.
//
//	mailer.To("you@example.com").Send(ctx, WelcomeEmail{})
//
// It returns a pending message rather than sending, so cc and bcc chain in the
// order they are read.
func (m *Mailer) To(addresses ...string) *Pending {
	p := &Pending{mailer: m}
	for _, a := range addresses {
		p.to = append(p.to, Address{Email: a})
	}
	return p
}

// ToAddress is To for a recipient whose display name is known.
func (m *Mailer) ToAddress(addresses ...Address) *Pending {
	return &Pending{mailer: m, to: addresses}
}

// Transport is which one is wired, for a health check or a log line.
func (m *Mailer) Transport() Transport { return m.transport }

// Pending is a message being addressed.
type Pending struct {
	mailer *Mailer
	to     []Address
	cc     []Address
	bcc    []Address
}

// CC and BCC add recipients.
func (p *Pending) CC(addresses ...string) *Pending {
	for _, a := range addresses {
		p.cc = append(p.cc, Address{Email: a})
	}
	return p
}

// BCC adds recipients nobody else sees.
func (p *Pending) BCC(addresses ...string) *Pending {
	for _, a := range addresses {
		p.bcc = append(p.bcc, Address{Email: a})
	}
	return p
}

// Send renders the mailable and hands it to the transport.
//
// It is synchronous. Sending on the queue is a job that calls this, and that is
// deliberate: a call that sometimes blocks for two seconds and sometimes does
// not, decided by an interface the mailable implements somewhere else, is a call
// nobody can reason about from the line they are reading.
func (p *Pending) Send(ctx context.Context, mailable Mailable) error {
	env := mailable.Envelope()

	env.To = append(env.To, p.to...)
	env.CC = append(env.CC, p.cc...)
	env.BCC = append(env.BCC, p.bcc...)
	if env.From.Email == "" {
		env.From = p.mailer.from
	}

	if len(env.To)+len(env.CC)+len(env.BCC) == 0 {
		return ErrNoRecipient
	}
	for _, a := range append(append(append([]Address{env.From}, env.To...), env.CC...), env.BCC...) {
		if !a.Valid() {
			return fmt.Errorf("mail: %q is not an address", a.Email)
		}
	}
	if strings.TrimSpace(env.Subject) == "" {
		return errors.New("mail: the envelope has no subject")
	}

	content := mailable.Content()
	msg := Message{Envelope: env, Text: content.Text}

	if content.View != "" {
		html, err := p.mailer.render.RenderToString(content.View, content.Data)
		if err != nil {
			return fmt.Errorf("mail: rendering %s: %w", content.View, err)
		}
		msg.HTML = html
	}
	if content.TextView != "" {
		text, err := p.mailer.render.RenderToString(content.TextView, content.Data)
		if err != nil {
			return fmt.Errorf("mail: rendering %s: %w", content.TextView, err)
		}
		msg.Text = text
	}
	if msg.HTML == "" && msg.Text == "" {
		return errors.New("mail: the message has no body")
	}

	return p.mailer.transport.Send(ctx, msg)
}
