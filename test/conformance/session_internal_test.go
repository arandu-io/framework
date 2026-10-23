package conformance

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/auth/account"
	"github.com/arandu-io/hesape/encryption"
)

// screens is a sign-in flow written the way the published screens behave,
// over the parts SessionMatrix hands it, with one defect switched on by name.
// The matrix has to pass the flow with no defect and fail it with each one.
type screens struct {
	parts  SessionParts
	signer *encryption.Signer
	defect string
}

// pendingState is what the pending cookie carries, as the published screens
// write it.
type pendingState struct {
	Tenant      string `json:"tenant"`
	UserID      string `json:"user_id"`
	Fingerprint string `json:"password_fingerprint"`
	Remember    bool   `json:"remember"`
}

const pendingCookie = "two-factor-pending"

func signInScreens(defect string) func(SessionParts) http.Handler {
	return func(p SessionParts) http.Handler {
		s := &screens{parts: p, signer: encryption.NewSigner(p.AppKey), defect: defect}
		mux := http.NewServeMux()
		mux.HandleFunc("POST /auth/login", s.login)
		mux.HandleFunc("POST /auth/two-factor/challenge", s.challenge(false))
		mux.HandleFunc("POST /auth/two-factor/recovery", s.challenge(true))
		return mux
	}
}

func (s *screens) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	user, err := s.parts.Users.VerifyCredentials(ctx, s.parts.Tenant, r.PostForm.Get("email"), r.PostForm.Get("password"), r.RemoteAddr)
	if err != nil {
		if s.defect == "a session on a wrong password" {
			found, _ := s.parts.Users.Lookup(ctx, s.parts.Tenant, r.PostForm.Get("email"))
			s.start(w, r, found)
			return
		}
		http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
		return
	}
	required, err := s.parts.Factors.Required(ctx, s.parts.Tenant, user.AccountID())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if required && s.defect != "the second factor skipped" {
		payload, _ := json.Marshal(pendingState{Tenant: s.parts.Tenant, UserID: user.AccountID(), Fingerprint: account.Fingerprint(user.PasswordHash())})
		http.SetCookie(w, &http.Cookie{
			Name: pendingCookie, Value: s.signer.Sign(pendingCookie, string(payload), 10*time.Minute),
			// Expires and no Max-Age, as some published screens write it.
			Path: "/auth/two-factor", Expires: time.Now().Add(10 * time.Minute), HttpOnly: true,
		})
		http.Redirect(w, r, "/auth/two-factor/challenge", http.StatusSeeOther)
		return
	}
	s.start(w, r, user)
}

func (s *screens) challenge(recovery bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		_ = r.ParseForm()
		state, ok := s.pending(r)
		if !ok {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		user, err := s.parts.Users.FindForAuthentication(ctx, state.Tenant, state.UserID)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		if account.Fingerprint(user.PasswordHash()) != state.Fingerprint && s.defect != "a changed password still signs in" {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		if recovery {
			err = s.parts.Factors.ConsumeRecovery(ctx, state.Tenant, state.UserID, r.PostForm.Get("recovery_code"))
		} else {
			err = s.parts.Factors.VerifyAuthenticator(ctx, state.Tenant, state.UserID, r.PostForm.Get("authenticator_code"))
		}
		var locked account.TwoFactorLockedError
		if errors.As(err, &locked) && s.defect != "a lock answered as a wrong code" {
			http.SetCookie(w, &http.Cookie{Name: pendingCookie, Path: "/auth/two-factor", MaxAge: -1})
			w.Header().Set("Retry-After", strconv.Itoa(locked.Seconds()))
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: pendingCookie, Path: "/auth/two-factor", MaxAge: -1})
		s.start(w, r, user)
	}
}

// pending reads the pending cookie, checking its signature and its expiry
// unless a defect says otherwise.
func (s *screens) pending(r *http.Request) (pendingState, bool) {
	cookie, err := r.Cookie(pendingCookie)
	if err != nil {
		return pendingState{}, false
	}
	payload, err := s.signer.Verify(pendingCookie, cookie.Value)
	switch {
	case err == nil:
	case s.defect == "an unsigned pending cookie accepted",
		s.defect == "an expired pending cookie accepted" && errors.Is(err, encryption.ErrExpired):
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(strings.Split(cookie.Value, ".")[0])
		if decodeErr != nil {
			return pendingState{}, false
		}
		payload = string(decoded)
	default:
		return pendingState{}, false
	}
	var state pendingState
	if json.Unmarshal([]byte(payload), &state) != nil {
		return pendingState{}, false
	}
	return state, true
}

func (s *screens) start(w http.ResponseWriter, r *http.Request, user account.Account) {
	subject := user.(*member).Subject()
	if s.defect == "a session for somebody else" {
		subject.ID = "user-b"
	}
	target := http.ResponseWriter(w)
	if s.defect == "the session cookie never sent" {
		target = httptest.NewRecorder()
	}
	if _, err := s.parts.Sessions.Start(r.Context(), target, subject); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func sessionFixture(defect string) SessionFixture {
	return SessionFixture{
		Account: func(hash string) account.Account {
			m := newMember(true)
			m.Password, m.Roles = hash, []string{"member"}
			return m
		},
		Handler: signInScreens(defect),
	}
}

func TestTheMatrixPassesScreensThatKeepTheSessionRules(t *testing.T) {
	SessionMatrix(t, sessionFixture(""))
}

func TestTheMatrixReadsTheFixturesSubjectWhenItGivesOne(t *testing.T) {
	f := sessionFixture("")
	read := 0
	f.Subject = func(a account.Account) auth.Subject { read++; return a.(*member).Subject() }
	SessionMatrix(t, f)
	if read == 0 {
		t.Fatal("the resolver never read the fixture's subject")
	}
}

func init() {
	for defect, want := range map[string][]string{
		"a session on a wrong password":       {"AWrongPasswordWritesNoSession", "session writes = 1, want 0"},
		"the second factor skipped":           {"ARightPasswordWithAFactorWritesOnlyThePendingCookie", "wrote no two-factor-pending cookie"},
		"a changed password still signs in":   {"APasswordChangedWhilePendingWritesNoSession", "session writes = 1, want 0"},
		"an unsigned pending cookie accepted": {"ATamperedPendingCookieWritesNoSession", "session writes = 1, want 0"},
		"an expired pending cookie accepted":  {"AnExpiredPendingCookieWritesNoSession", "session writes = 1, want 0"},
		"a lock answered as a wrong code":     {"ALockedAuthenticatorChallengeEndsThePendingSignIn", "want 303 to /auth/login"},
		"a session for somebody else":         {"ARightPasswordWithoutAFactorWritesOneSession", `the session was written for "user-b"`},
		"the session cookie never sent":       {"ARightPasswordWithoutAFactorWritesOneSession", "the session the sign-in wrote does not load"},
	} {
		broken["SessionMatrix/"+defect] = brokenCase{run: func(t *testing.T) { SessionMatrix(t, sessionFixture(defect)) }, want: want}
	}
	broken["SessionMatrix/no handler"] = brokenCase{
		run:  func(t *testing.T) { SessionMatrix(t, SessionFixture{Account: sessionFixture("").Account}) },
		want: []string{"conformance.SessionMatrix needs Account and Handler"},
	}
}
