// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape, against the
// code that now runs; the unit tests that used to live here were tests of an
// implementation this package no longer holds, and keeping a second copy of
// them would be a second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip
// per envelope, because an envelope is the place a rename can be wired to the
// wrong method and still compile.

package mail_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/mail"
	hmail "github.com/arandu-io/hesape/mail"
)

// TestAliasesAreTheHesapeSymbols is the whole of the alias half of this bridge.
//
// Every line is a compile-time assertion that the two names are one type or one
// value. A rename in hesape that this package has not followed fails here
// rather than in the thirteen repositories that import these names.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	// ErrRetryable cannot be constructed from outside hesape, so the identity
	// is asserted by a conversion the compiler has to accept.
	var carry func(hmail.ErrRetryable) mail.ErrRetryable = func(e hmail.ErrRetryable) mail.ErrRetryable {
		return e
	}
	_ = carry

	var renderer mail.Renderer = views{}
	var _ hmail.Renderer = renderer

	if !errors.Is(mail.ErrNoRecipient, hmail.ErrNoRecipient) {
		t.Error("ErrNoRecipient is not hesape's: a caller comparing against it answers the wrong thing")
	}
}

// TestAMessageReachesTheTransportThroughTheBridge is the round trip of the
// three envelopes a send crosses: the Mailable, the Content and the Message.
func TestAMessageReachesTheTransportThroughTheBridge(t *testing.T) {
	box := &mail.Array{}
	mailer := mail.New(box, views{}, mail.Address{Email: "app@example.test", Name: "Arandu"})

	err := mailer.
		ToAddress(mail.Address{Email: "reader@example.test", Name: "Ada"}).
		CC("copied@example.test").
		BCC("hidden@example.test").
		Send(context.Background(), welcome{Name: "Ada"})
	if err != nil {
		t.Fatalf("sending: %v", err)
	}

	sent, ok := box.Last()
	if !ok {
		t.Fatal("the array transport kept nothing: the bridge did not reach hesape's storage")
	}
	if len(sent.To) != 1 || sent.To[0].Email != "reader@example.test" || sent.To[0].Name != "Ada" {
		t.Errorf("the recipient did not survive the translation: %+v", sent.To)
	}
	if len(sent.CC) != 1 || sent.CC[0].Email != "copied@example.test" {
		t.Errorf("cc did not survive the translation: %+v", sent.CC)
	}
	if len(sent.BCC) != 1 || sent.BCC[0].Email != "hidden@example.test" {
		t.Errorf("bcc did not survive the translation: %+v", sent.BCC)
	}
	if sent.From.Email != "app@example.test" || sent.From.Name != "Arandu" {
		t.Errorf("the default sender did not survive the translation: %+v", sent.From)
	}
	if sent.Subject != "Welcome" {
		t.Errorf("the subject is %q", sent.Subject)
	}
	if sent.HTML != "rendered mail.welcome" {
		t.Errorf("the HTML part is %q: Content.View did not reach hesape's View", sent.HTML)
	}
	if sent.Text != "rendered mail.welcome-text" {
		t.Errorf("the text part is %q: Content.TextView did not reach hesape's Text", sent.Text)
	}
	if len(sent.Tags) != 1 || sent.Tags[0] != "welcome" {
		t.Errorf("the tags did not survive the translation: %+v", sent.Tags)
	}
}

// TestTheLiteralTextPartIsApplied covers the one field with no counterpart on
// the other side: hesape's Content.Text is a view name, and this package's
// Content.Text is a body.
func TestTheLiteralTextPartIsApplied(t *testing.T) {
	box := &mail.Array{}
	mailer := mail.New(box, views{}, mail.Address{Email: "app@example.test"})

	if err := mailer.To("reader@example.test").Send(context.Background(), literal{}); err != nil {
		t.Fatalf("sending: %v", err)
	}
	sent, _ := box.Last()
	if sent.Text != "the body, written out" {
		t.Errorf("the literal text part is %q", sent.Text)
	}
	if sent.HTML != "rendered mail.welcome" {
		t.Errorf("the HTML part is %q: the literal text callback overwrote it", sent.HTML)
	}
}

// TestTheTextViewWinsOverTheLiteral is the precedence this package documented
// on Content.Text, checked across the boundary because the two fields land in
// different places on the other side.
func TestTheTextViewWinsOverTheLiteral(t *testing.T) {
	box := &mail.Array{}
	mailer := mail.New(box, views{}, mail.Address{Email: "app@example.test"})

	if err := mailer.To("reader@example.test").Send(context.Background(), both{}); err != nil {
		t.Fatalf("sending: %v", err)
	}
	sent, _ := box.Last()
	if sent.Text != "rendered mail.welcome-text" {
		t.Errorf("the text part is %q: the literal won, and TextView should have", sent.Text)
	}
}

// TestTheRefusalsSurvive are the three failures this package answered before it
// reached a transport, and answers the same way now.
func TestTheRefusalsSurvive(t *testing.T) {
	box := &mail.Array{}
	mailer := mail.New(box, views{}, mail.Address{Email: "app@example.test"})
	ctx := context.Background()

	if err := mailer.To().Send(ctx, welcome{}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Errorf("a message with no recipient answered %v", err)
	}
	if err := mailer.To("not an address").Send(ctx, welcome{}); err == nil ||
		!strings.Contains(err.Error(), "is not an address") {
		t.Errorf("a bad address answered %v", err)
	}
	err := mailer.To("reader@example.test").Send(ctx, silent{})
	if err == nil || err.Error() != "mail: the envelope has no subject" {
		t.Errorf("a message with no subject answered %v, and hesape would have invented a subject", err)
	}
	if len(box.Sent()) != 0 {
		t.Error("a refused message reached the transport")
	}
}

// TestTheArrayTransportKeepsTheOldNames is the envelope over the transport
// whose three readers were renamed.
func TestTheArrayTransportKeepsTheOldNames(t *testing.T) {
	box := &mail.Array{}
	mailer := mail.New(box, views{}, mail.Address{Email: "app@example.test"})
	ctx := context.Background()

	if mailer.Transport() != mail.Transport(box) {
		t.Error("Transport answered something other than the value New was given")
	}
	for range 2 {
		if err := mailer.To("reader@example.test").Send(ctx, welcome{}); err != nil {
			t.Fatalf("sending: %v", err)
		}
	}
	if got := len(box.Sent()); got != 2 {
		t.Errorf("Sent answered %d messages: it is Messages on the other side", got)
	}
	box.Reset()
	if got := len(box.Sent()); got != 0 {
		t.Errorf("Reset left %d messages: it is Flush on the other side", got)
	}
	if _, ok := box.Last(); ok {
		t.Error("Last answered a message after Reset")
	}
}

// TestRenderReachesTheWireFormat is the one function in this package that is a
// wrapper rather than a type.
func TestRenderReachesTheWireFormat(t *testing.T) {
	raw := mail.Render(mail.Message{
		Envelope: mail.Envelope{
			From:    mail.Address{Email: "app@example.test", Name: "Arandu"},
			To:      []mail.Address{{Email: "reader@example.test"}},
			BCC:     []mail.Address{{Email: "hidden@example.test"}},
			Subject: "Welcome",
		},
		Text: "hello",
	})
	if !strings.Contains(raw, "To: reader@example.test") {
		t.Errorf("the recipient is not in the headers:\n%s", raw)
	}
	if strings.Contains(raw, "hidden@example.test") {
		t.Errorf("bcc reached the headers:\n%s", raw)
	}
}

// TestAProviderFailureIsStillRetryable proves the error identity, which is what
// the ErrRetryable alias is for: a job outside this module reads the marker off
// an error a hesape transport produced.
func TestAProviderFailureIsStillRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	err := mail.Resend{Key: "re_test", Endpoint: server.URL}.Send(context.Background(), mail.Message{
		Envelope: mail.Envelope{
			From:    mail.Address{Email: "app@example.test"},
			To:      []mail.Address{{Email: "reader@example.test"}},
			Subject: "Welcome",
		},
		Text: "hello",
	})

	var retryable mail.ErrRetryable
	if !errors.As(err, &retryable) {
		t.Errorf("a rate limit answered %v, which no job can tell from a rejected address", err)
	}
}

// TestTheShippedTransportsAnswerTheirNames is the shell around each of the five,
// checked by the one method that needs no network.
func TestTheShippedTransportsAnswerTheirNames(t *testing.T) {
	for want, transport := range map[string]mail.Transport{
		"smtp":     mail.SMTP{Host: "localhost", Port: "587"},
		"log":      mail.Log{},
		"array":    &mail.Array{},
		"resend":   mail.Resend{},
		"sendgrid": mail.SendGrid{},
	} {
		if got := transport.Name(); got != want {
			t.Errorf("the transport named %q answers %q", want, got)
		}
	}
}

// views is a Renderer that says which view it was asked for, so that a test can
// tell the HTML part from the text part without a view registry.
type views struct{}

func (views) RenderToString(name string, _ any) (string, error) {
	return "rendered " + name, nil
}

type welcome struct{ Name string }

func (welcome) Envelope() mail.Envelope {
	return mail.Envelope{Subject: "Welcome", Tags: []string{"welcome"}}
}

func (m welcome) Content() mail.Content {
	return mail.Content{View: "mail.welcome", TextView: "mail.welcome-text", Data: m}
}

type literal struct{}

func (literal) Envelope() mail.Envelope { return mail.Envelope{Subject: "Welcome"} }

func (m literal) Content() mail.Content {
	return mail.Content{View: "mail.welcome", Text: "the body, written out", Data: m}
}

type both struct{}

func (both) Envelope() mail.Envelope { return mail.Envelope{Subject: "Welcome"} }

func (m both) Content() mail.Content {
	return mail.Content{
		View:     "mail.welcome",
		TextView: "mail.welcome-text",
		Text:     "the body, written out",
		Data:     m,
	}
}

type silent struct{}

func (silent) Envelope() mail.Envelope { return mail.Envelope{} }

func (m silent) Content() mail.Content { return mail.Content{View: "mail.welcome", Data: m} }
