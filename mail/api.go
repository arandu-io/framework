package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The two provider transports, and why they are here and not in a submodule.
//
// The comment on the SMTP transport says an adapter belongs in a module of its
// own, because a provider's SDK in the core is that SDK in every binary. That is
// true of an SDK. It is not true of these two: both providers are one POST with
// a JSON body, so what they cost the core is net/http and encoding/json, which
// every Arandu binary already links.
//
// Writing them against the SDK would have bought nothing and cost a dependency
// tree in `go.sum` -- resend-go alone brings its own transitive set -- for a
// request that fits on a screen. See ADR 0031.
//
// Amazon SES is deliberately not here, and not anywhere: it is the one that does
// need a signing implementation, and the one nobody asked for.

// ErrRetryable marks a failure worth trying again.
//
// A 429 or a 5xx from a provider is not the same event as a rejected address,
// and treating them alike is how a verification e-mail is silently lost during a
// rate limit. A job that sends checks for this and reschedules; a request that
// sends inline reports it and moves on.
type ErrRetryable struct{ err error }

func (e ErrRetryable) Error() string { return e.err.Error() }
func (e ErrRetryable) Unwrap() error { return e.err }

// Resend sends through resend.com.
//
// It is the default recommendation for an application that has outgrown the log
// transport: a domain, a DNS record and an API key, and no server to run.
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

// Name identifies the transport in a log line.
func (Resend) Name() string { return "resend" }

// Send posts the message.
func (t Resend) Send(ctx context.Context, m Message) error {
	body := map[string]any{
		"from":    m.From.String(),
		"to":      addressList(m.To),
		"subject": m.Subject,
	}
	if m.HTML != "" {
		body["html"] = m.HTML
	}
	if m.Text != "" {
		body["text"] = m.Text
	}
	if len(m.CC) > 0 {
		body["cc"] = addressList(m.CC)
	}
	if len(m.BCC) > 0 {
		body["bcc"] = addressList(m.BCC)
	}
	if len(m.ReplyTo) > 0 {
		body["reply_to"] = addressList(m.ReplyTo)
	}
	// Resend's tags are key/value and reject anything outside [A-Za-z0-9_-], so
	// an envelope tag becomes the value of a "tag" key rather than being sent
	// raw and rejected as a whole message.
	for _, tag := range m.Tags {
		body["tags"] = append(asSlice(body["tags"]), map[string]string{"name": "tag", "value": slug(tag)})
	}

	return post(ctx, t.Client, t.Timeout, http.MethodPost,
		endpointOr(t.Endpoint, "https://api.resend.com/emails"),
		map[string]string{"Authorization": "Bearer " + t.Key},
		body, resendError)
}

// SendGrid sends through sendgrid.com.
//
// The second provider rather than the only one, because a transport with one
// implementation is an interface nobody has proved is an interface.
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

// Name identifies the transport in a log line.
func (SendGrid) Name() string { return "sendgrid" }

// Send posts the message.
func (t SendGrid) Send(ctx context.Context, m Message) error {
	// One personalization holding every recipient. SendGrid's other reading of
	// this field -- one personalization per recipient -- is a different feature
	// (a separate message each, with its own substitutions), and using it here
	// would turn one message to three people into three messages.
	to := make([]map[string]string, 0, len(m.To))
	for _, a := range m.To {
		to = append(to, sendgridAddress(a))
	}
	person := map[string]any{"to": to}
	if len(m.CC) > 0 {
		person["cc"] = sendgridAddresses(m.CC)
	}
	if len(m.BCC) > 0 {
		person["bcc"] = sendgridAddresses(m.BCC)
	}

	// Text before HTML. SendGrid sends the parts in the order they arrive, and
	// MIME says the richest alternative comes last -- reversed, every client
	// shows the plain-text version of a message that has HTML.
	content := make([]map[string]string, 0, 2)
	if m.Text != "" {
		content = append(content, map[string]string{"type": "text/plain", "value": m.Text})
	}
	if m.HTML != "" {
		content = append(content, map[string]string{"type": "text/html", "value": m.HTML})
	}

	body := map[string]any{
		"personalizations": []any{person},
		"from":             sendgridAddress(m.From),
		"subject":          m.Subject,
		"content":          content,
	}
	if len(m.ReplyTo) > 0 {
		body["reply_to"] = sendgridAddress(m.ReplyTo[0])
	}
	if len(m.Tags) > 0 {
		body["categories"] = m.Tags
	}

	return post(ctx, t.Client, t.Timeout, http.MethodPost,
		endpointOr(t.Endpoint, "https://api.sendgrid.com/v3/mail/send"),
		map[string]string{"Authorization": "Bearer " + t.Key},
		body, sendgridError)
}

// post is the whole of both transports that is not the body.
func post(ctx context.Context, client *http.Client, timeout time.Duration,
	method, url string, headers map[string]string, body any,
	describe func(status int, body []byte) string) error {

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("mail: encoding the message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if client == nil {
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		// The request never got an answer: a DNS failure, a refused connection,
		// a timeout. Every one of those is worth retrying.
		return ErrRetryable{fmt.Errorf("mail: %w", err)}
	}
	defer resp.Body.Close()

	// Bounded, because an error body is the one place a provider has no reason
	// to be large and every reason to be attacker-influenced.
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	failure := fmt.Errorf("mail: the provider answered %d: %s", resp.StatusCode, describe(resp.StatusCode, answer))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return ErrRetryable{failure}
	}
	return failure
}

// resendError reads {"statusCode":..,"message":..,"name":..}.
func resendError(_ int, body []byte) string {
	var out struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Message == "" {
		return truncate(string(body))
	}
	if out.Name != "" {
		return out.Name + ": " + out.Message
	}
	return out.Message
}

// sendgridError reads {"errors":[{"message":..,"field":..}]}.
func sendgridError(_ int, body []byte) string {
	var out struct {
		Errors []struct {
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Errors) == 0 {
		return truncate(string(body))
	}
	parts := make([]string, 0, len(out.Errors))
	for _, e := range out.Errors {
		if e.Field != "" {
			parts = append(parts, e.Field+": "+e.Message)
			continue
		}
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, "; ")
}

func addressList(list []Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}
	return out
}

func sendgridAddress(a Address) map[string]string {
	out := map[string]string{"email": a.Email}
	if a.Name != "" {
		out["name"] = a.Name
	}
	return out
}

func sendgridAddresses(list []Address) []map[string]string {
	out := make([]map[string]string, 0, len(list))
	for _, a := range list {
		out = append(out, sendgridAddress(a))
	}
	return out
}

func asSlice(v any) []map[string]string {
	if out, ok := v.([]map[string]string); ok {
		return out
	}
	return nil
}

// slug reduces a tag to what a provider accepts in one.
func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no answer body"
	}
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

func endpointOr(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

var (
	_ Transport = Resend{}
	_ Transport = SendGrid{}
)
