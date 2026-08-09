package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/mail"
)

// provider is a stand-in for resend.com or sendgrid.com: it records the request
// and answers with what the test asked it to.
type provider struct {
	body   map[string]any
	auth   string
	status int
	answer string
}

func (p *provider) serve(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &p.body)

		if p.status == 0 {
			p.status = http.StatusOK
		}
		w.WriteHeader(p.status)
		_, _ = io.WriteString(w, p.answer)
	}))
	t.Cleanup(s.Close)
	return s
}

// note is a mailable with both parts, which is the shape every message the
// framework itself sends has.
type note struct{ to string }

func (note) Envelope() mail.Envelope {
	return mail.Envelope{
		From:    mail.Address{Email: "blog@example.com", Name: "The Blog"},
		Subject: "Confirm your address",
		Tags:    []string{"account verification"},
	}
}

func (note) Content() mail.Content {
	return mail.Content{Text: "Open the link.", Data: nil}
}

func send(t *testing.T, transport mail.Transport, to string) error {
	t.Helper()
	return mail.New(transport, nil, mail.Address{Email: "blog@example.com"}).
		To(to).Send(context.Background(), note{to: to})
}

// TestResendSendsWhatTheEnvelopeSaid.
//
// The body is the whole transport. A field renamed by the provider is a message
// that is accepted and delivered to nobody, or delivered without its subject.
func TestResendSendsWhatTheEnvelopeSaid(t *testing.T) {
	p := &provider{answer: `{"id":"abc"}`}
	server := p.serve(t)

	if err := send(t, mail.Resend{Key: "re_test", Endpoint: server.URL}, "reader@example.com"); err != nil {
		t.Fatalf("the message was not accepted: %v", err)
	}

	if p.auth != "Bearer re_test" {
		t.Errorf("the key did not reach the provider: %q", p.auth)
	}
	if got := p.body["from"]; got != `"The Blog" <blog@example.com>` {
		t.Errorf("from is %v", got)
	}
	if got := p.body["subject"]; got != "Confirm your address" {
		t.Errorf("subject is %v", got)
	}
	to, _ := p.body["to"].([]any)
	if len(to) != 1 || to[0] != "reader@example.com" {
		t.Errorf("to is %v", p.body["to"])
	}
	if _, ok := p.body["html"]; ok {
		t.Error("a message with no HTML part sent an empty one, which renders as a blank e-mail")
	}
}

// TestResendTagsAreReducedToWhatTheProviderAccepts.
//
// Resend rejects a tag containing anything outside [A-Za-z0-9_-], and it rejects
// the whole message rather than the tag. A space in a tag would drop the
// e-mail -- a verification link lost to a label.
func TestResendTagsAreReducedToWhatTheProviderAccepts(t *testing.T) {
	p := &provider{answer: `{"id":"abc"}`}
	server := p.serve(t)

	if err := send(t, mail.Resend{Key: "k", Endpoint: server.URL}, "reader@example.com"); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(p.body["tags"])
	if strings.Contains(string(raw), " ") {
		t.Errorf("a tag went out with a space in it: %s", raw)
	}
	if !strings.Contains(string(raw), "account_verification") {
		t.Errorf("the tag did not survive: %s", raw)
	}
}

// TestARateLimitIsRetryableAndARejectedAddressIsNot.
//
// They are different events and the caller acts differently on them. A 429
// treated as final is a verification e-mail silently lost; a bad address treated
// as retryable is a job that fails forever.
func TestARateLimitIsRetryableAndARejectedAddressIsNot(t *testing.T) {
	for _, c := range []struct {
		name    string
		status  int
		retries bool
	}{
		{"rate limited", http.StatusTooManyRequests, true},
		{"provider down", http.StatusBadGateway, true},
		{"rejected", http.StatusUnprocessableEntity, false},
		{"bad key", http.StatusUnauthorized, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &provider{status: c.status, answer: `{"name":"x","message":"nope"}`}
			server := p.serve(t)

			err := send(t, mail.Resend{Key: "k", Endpoint: server.URL}, "reader@example.com")
			if err == nil {
				t.Fatal("a failure was reported as a send")
			}

			var retryable mail.ErrRetryable
			if got := errors.As(err, &retryable); got != c.retries {
				t.Errorf("retryable is %v, want %v: %v", got, c.retries, err)
			}
		})
	}
}

// TestTheProvidersMessageReachesTheCaller: "the provider answered 422" alone
// sends somebody to a dashboard to find out what was wrong with the address.
func TestTheProvidersMessageReachesTheCaller(t *testing.T) {
	p := &provider{status: 422, answer: `{"name":"validation_error","message":"The domain is not verified"}`}
	server := p.serve(t)

	err := send(t, mail.Resend{Key: "k", Endpoint: server.URL}, "reader@example.com")
	if err == nil || !strings.Contains(err.Error(), "domain is not verified") {
		t.Fatalf("the provider's reason did not reach the caller: %v", err)
	}
}

// TestSendGridPutsTextBeforeHTML.
//
// SendGrid sends the parts in the order they arrive and MIME says the richest
// alternative comes last. Reversed, every client shows the plain-text version of
// a message that has HTML -- which looks like a broken template, not a bug here.
func TestSendGridPutsTextBeforeHTML(t *testing.T) {
	p := &provider{status: http.StatusAccepted}
	server := p.serve(t)

	err := mail.New(mail.SendGrid{Key: "SG.k", Endpoint: server.URL}, fixedView{}, mail.Address{Email: "blog@example.com"}).
		To("reader@example.com").Send(context.Background(), bothParts{})
	if err != nil {
		t.Fatalf("the message was not accepted: %v", err)
	}

	content, _ := p.body["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("both parts did not arrive: %v", p.body["content"])
	}
	first, _ := content[0].(map[string]any)
	if first["type"] != "text/plain" {
		t.Errorf("the first part is %v: the client will show it instead of the HTML", first["type"])
	}
}

// TestSendGridSendsOneMessageAndNotOnePerRecipient.
//
// One personalization per recipient is a different SendGrid feature -- a
// separate message each -- and using it here turns one message to three people
// into three messages, each of which looks fine on its own.
func TestSendGridSendsOneMessageAndNotOnePerRecipient(t *testing.T) {
	p := &provider{status: http.StatusAccepted}
	server := p.serve(t)

	err := mail.New(mail.SendGrid{Key: "SG.k", Endpoint: server.URL}, fixedView{}, mail.Address{Email: "blog@example.com"}).
		To("a@example.com", "b@example.com").CC("c@example.com").
		Send(context.Background(), bothParts{})
	if err != nil {
		t.Fatal(err)
	}

	people, _ := p.body["personalizations"].([]any)
	if len(people) != 1 {
		t.Fatalf("%d personalizations: that is %d separate messages", len(people), len(people))
	}
	first, _ := people[0].(map[string]any)
	if to, _ := first["to"].([]any); len(to) != 2 {
		t.Errorf("%d recipients in the personalization, want 2", len(to))
	}
	if _, ok := first["cc"]; !ok {
		t.Error("the cc was dropped")
	}
}

// bothParts is a mailable with an HTML part and a text part.
type bothParts struct{}

func (bothParts) Envelope() mail.Envelope {
	return mail.Envelope{From: mail.Address{Email: "blog@example.com"}, Subject: "Both"}
}
func (bothParts) Content() mail.Content {
	return mail.Content{View: "mail.note", Text: "the text part"}
}

// fixedView is a Renderer that answers with one string.
type fixedView struct{}

func (fixedView) RenderToString(string, any) (string, error) { return "<p>the html part</p>", nil }
