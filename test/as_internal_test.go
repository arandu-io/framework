package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
	hhttp "github.com/arandu-io/hesape/http"
)

func store(resolve hhttp.SubjectResolver) *hhttp.SessionStore {
	s := hhttp.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, nil)
	if resolve != nil {
		s.ResolveSubjects(resolve)
	}
	return s
}

func onlyU1(_ context.Context, who auth.Subject) (auth.Subject, error) {
	if who.ID != "u1" {
		return auth.Subject{}, hhttp.ErrSubjectGone
	}
	return auth.Subject{ID: who.ID, Tenant: who.Tenant, Roles: []string{"admin"}}, nil
}

// What As refuses, each with the sentence that says why: As fails the test,
// so the refusals are read here, from the function it fails with.
func TestSignInRefuses(t *testing.T) {
	for _, c := range []struct {
		name     string
		sessions *hhttp.SessionStore
		who      auth.Subject
		says     string
	}{
		{"no session store", nil, auth.Subject{ID: "u1", Tenant: "acme"}, "no session store"},
		{"a guest", store(onlyU1), auth.Guest("acme"), "guest"},
		{"no id", store(onlyU1), auth.Subject{Tenant: "acme"}, "ID and its Tenant"},
		{"no tenant", store(onlyU1), auth.Subject{ID: "u1"}, "ID and its Tenant"},
		{"roles written in the test", store(onlyU1), auth.Subject{ID: "u1", Tenant: "acme", Roles: []string{"admin"}}, "SubjectResolver's to answer"},
		{"actions written in the test", store(onlyU1), auth.Subject{ID: "u1", Tenant: "acme", Actions: []auth.Action{"invoice.view"}}, "SubjectResolver's to answer"},
		{"an impersonation", store(onlyU1), auth.Subject{ID: "u1", Tenant: "acme", Impersonator: &auth.Subject{ID: "op", Tenant: "acme"}}, "impersonation"},
		{"an account the resolver does not know", store(onlyU1), auth.Subject{ID: "u9", Tenant: "acme"}, "create the account"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cookies, err := signIn(t.Context(), c.sessions, c.who)
			if err == nil {
				t.Fatalf("signIn accepted it and handed back %d cookies", len(cookies))
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal %q does not say %q", err, c.says)
			}
		})
	}
}

func TestSignInHandsBackTheSessionCookie(t *testing.T) {
	cookies, err := signIn(t.Context(), store(onlyU1), auth.Subject{ID: "u1", Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Errorf("signIn handed back %v", cookies)
	}
}
