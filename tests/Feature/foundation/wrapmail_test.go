package feature

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/mail"
)

// watching counts what passes through it on the way to next.
type watching struct {
	next mail.Transport
	seen []string
}

func (w *watching) Name() string { return "watching" }

func (w *watching) Send(ctx context.Context, m mail.Message) error {
	w.seen = append(w.seen, m.Subject)
	return w.next.Send(ctx, m)
}

type receipt struct{}

func (receipt) Envelope() mail.Envelope { return mail.Envelope{Subject: "Receipt"} }
func (receipt) Content() mail.Content   { return mail.Content{Text: "Paid in full."} }

// TestWrapMailPutsATransportAroundTheConfiguredOne: a message sent through
// Mail passes the wrapper and still reaches the transport MAIL_URL named,
// from the configured sender.
func TestWrapMailPutsATransportAroundTheConfiguredOne(t *testing.T) {
	d, err := foundation.NewDriversOver(settingsFor(t, "MAIL_URL", "array://", "MAIL_FROM_ADDRESS", "app@example.test"), pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}
	box := d.Mail.Transport().(*mail.Array)

	var w *watching
	if err := d.WrapMail(func(next mail.Transport) (mail.Transport, error) {
		w = &watching{next: next}
		return w, nil
	}); err != nil {
		t.Fatalf("WrapMail: %v", err)
	}
	if err := d.Mail.To("ana@example.test").Send(context.Background(), receipt{}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(w.seen) != 1 || w.seen[0] != "Receipt" {
		t.Errorf("the wrapper saw %v, want the one message", w.seen)
	}
	sent, ok := box.Last()
	if !ok || sent.From.Email != "app@example.test" {
		t.Errorf("the configured transport kept %+v (ok=%v), want the message from the configured sender", sent, ok)
	}

	before := d.Mail
	if err := d.WrapMail(func(mail.Transport) (mail.Transport, error) { return nil, errors.New("no logger") }); err == nil {
		t.Error("a wrap that failed was accepted")
	}
	if err := d.WrapMail(func(mail.Transport) (mail.Transport, error) { return nil, nil }); err == nil {
		t.Error("a wrap that answered no transport was accepted")
	}
	if d.Mail != before {
		t.Error("a refused wrap replaced the mailer")
	}
}
