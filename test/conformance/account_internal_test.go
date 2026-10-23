package conformance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/auth/account/accounttest"
	"github.com/arandu-io/hesape/database/model"
)

// What the account rules do to rows is proved against real engines, by the
// applications that call ModelPersistence and by accounttest in the
// connectors. What is proved here is the model check: what a user model's
// JSON and session subject must say about the verification.

// member is a user model as an application writes it, with the ways a model
// can contradict itself as switches.
type member struct {
	model.Model

	ID         string     `db:"id" json:"id"`
	TenantID   string     `db:"tenant_id" json:"tenant_id"`
	Name       string     `db:"name" json:"name"`
	Email      string     `db:"email" json:"email"`
	Password   string     `db:"password" json:"-"`
	VerifiedAt *time.Time `db:"verified_at" json:"verified_at,omitempty"`
	Roles      []string   `db:"roles" json:"roles"`

	leakHash       bool
	subjectIgnores bool
}

func (m *member) AccountID() string     { return m.ID }
func (m *member) AccountTenant() string { return m.TenantID }
func (m *member) AccountEmail() string  { return m.Email }
func (m *member) AccountName() string   { return m.Name }
func (m *member) PasswordHash() string  { return m.Password }
func (m *member) IsVerified() bool      { return m.VerifiedAt != nil }

func (m *member) Subject() auth.Subject {
	return auth.Subject{ID: m.ID, Tenant: m.TenantID, Roles: m.Roles, Verified: m.IsVerified() && !m.subjectIgnores}
}

// MarshalJSON is the model's own JSON, and with leakHash it carries the hash
// under another name, which a check that only looks for "password" misses.
func (m *member) MarshalJSON() ([]byte, error) {
	type plain member
	encoded, err := json.Marshal((*plain)(m))
	if err != nil || !m.leakHash {
		return encoded, err
	}
	return []byte(strings.TrimSuffix(string(encoded), "}") + `,"secret":"` + m.Password + `"}`), nil
}

func newMember(verified bool) *member {
	m := &member{ID: "user-a", TenantID: "tenant-a", Name: "Ana", Email: "ana@example.test", Password: "$argon2id$v=19$hash"}
	if verified {
		at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		m.VerifiedAt = &at
	}
	return m
}

func TestAModelThatAgreesWithItselfPasses(t *testing.T) {
	for _, verified := range []bool{false, true} {
		if problems := verificationProblems(ModelFixture{}, newMember(verified), verified); len(problems) != 0 {
			t.Fatalf("verified=%v: %v", verified, problems)
		}
	}
}

func TestAModelThatContradictsItselfIsNamed(t *testing.T) {
	for name, c := range map[string]struct {
		account *member
		fixture ModelFixture
		want    bool
		says    string
	}{
		"the subject ignores the verification": {
			func() *member { m := newMember(true); m.subjectIgnores = true; return m }(), ModelFixture{}, true,
			"the session subject's Verified = false, want true",
		},
		"the JSON carries the hash": {
			func() *member { m := newMember(false); m.leakHash = true; return m }(), ModelFixture{}, false,
			"the account's JSON carries the password hash",
		},
		"the model says the wrong thing": {
			newMember(false), ModelFixture{}, true,
			"IsVerified() = false, want true",
		},
		"the fixture's subject is read instead": {
			newMember(true), ModelFixture{Subject: func(a account.Account) auth.Subject { return auth.Subject{ID: a.AccountID()} }}, true,
			"the session subject's Verified = false, want true",
		},
	} {
		t.Run(name, func(t *testing.T) {
			problems := verificationProblems(c.fixture, c.account, c.want)
			found := false
			for _, p := range problems {
				found = found || strings.Contains(p, c.says)
			}
			if !found {
				t.Fatalf("verificationProblems = %q, want one saying %q", problems, c.says)
			}
		})
	}
}

func TestAModelWithNoSubjectMethodAsksForTheFixturesFunction(t *testing.T) {
	// Embedding the interface keeps its methods and nothing else.
	type bare struct{ account.Account }
	_, err := subjectFrom(nil, bare{newMember(true)})
	if err == nil || !strings.Contains(err.Error(), "has no Subject method; give the fixture a Subject function") {
		t.Fatalf("subjectFrom answered %v", err)
	}
}

func init() {
	broken["ModelPersistence/rules that cannot be built"] = brokenCase{
		run: func(t *testing.T) {
			ModelPersistence(t, func(*testing.T) ModelFixture { return ModelFixture{} })
		},
		want: []string{"TheAccountReadBackIsTheModelTheQueryBuilt", "building the account rules"},
	}
	broken["ModelPersistence/a role without the function that grants it"] = brokenCase{
		run: func(t *testing.T) {
			ModelPersistence(t, func(*testing.T) ModelFixture { return ModelFixture{Role: "admin"} })
		},
		want: []string{"RolesAreOneValueInGoAndJSONInTheColumn", "ModelFixture needs both Role and GrantRole"},
	}
	broken["Account/rules that cannot be built"] = brokenCase{
		run:  func(t *testing.T) { Account(t, func(*testing.T) accounttest.Fixture { return accounttest.Fixture{} }) },
		want: []string{"--- FAIL: TestTheBrokenApplicationRunsInAChild/"},
	}
}
