package feature

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/translation"
	"github.com/arandu-io/hesape/validation"
)

// brRules is a module's provider, written the way a module writes one. Its
// checks are stand-ins: what these tests are about is how the rules reach the
// person, not how a CPF is computed.
type brRules struct{}

func (brRules) RegisterValidation(r *validation.Registry) {
	digits := func(n int) validation.ExtensionFunc {
		return func(_ *validation.Validator, _ string, value any, _ []string) bool {
			s, _ := value.(string)
			return len(s) == n && strings.Trim(s, "0123456789") == ""
		}
	}
	r.Add(validation.RuleDef{
		Name: "cpf", Check: digits(11),
		Messages: map[string]string{"pt-BR": "O campo :attribute não é um CPF válido.", "en": "The :attribute field is not a valid CPF."},
	})
	r.Add(validation.RuleDef{
		Name: "formato_cep", Check: digits(8),
		Messages: map[string]string{"pt-BR": "O campo :attribute não tem o formato de um CEP.", "en": "The :attribute field is not a CEP."},
	})
	r.Add(validation.RuleDef{
		Name: "celular_com_ddd", Check: digits(11),
		Messages: map[string]string{"pt-BR": "O campo :attribute deve ser um celular com DDD.", "en": "The :attribute field must be a mobile phone with area code."},
	})
}

// registerPerson is a Form Request mixing the two forms a rule list is written
// in.
type registerPerson struct {
	CPF     string `form:"cpf"`
	CEP     string `form:"cep"`
	Celular string `form:"celular"`
}

func (registerPerson) Rules() validation.Rules {
	return validation.Rules{
		"cpf":     "required|cpf",
		"cep":     []any{"required", "formato_cep"},
		"celular": "nullable|celular_com_ddd",
	}
}

func (registerPerson) Attributes() map[string]string { return map[string]string{"cpf": "CPF"} }

// typoRequest names a rule no provider declares.
type typoRequest struct{}

func (typoRequest) Rules() validation.Rules {
	return validation.Rules{"cpf": "required|cpff"}
}

type peopleModule struct{}

func (peopleModule) Name() string { return "people" }

func (peopleModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodPost, "/people", func(ctx *fhttp.Context) error {
		var in registerPerson
		if err := ctx.Bind(&in); err != nil {
			return err
		}
		ctx.Response.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(ctx.Response).Encode(map[string]any{"validated": ctx.Validated().Tree(), "bound": in})
	})
}

// portuguese is the application's catalogue: pt-BR first, with the sentences
// the framework ships behind it.
func portuguese() *translation.Translator { return translation.New(nil, "pt-BR", "en") }

// peopleApp is bootstrap/app.go of an application that validates in Portuguese:
// a translator, the module's provider and the request types to check.
func peopleApp(t *testing.T, wire func(*foundation.Application)) *foundation.Application {
	t.Helper()
	app := foundation.New(testConfig(config.EnvProd)).
		WithTranslator(portuguese()).
		WithValidation(brRules{}).
		WithValidationRequests(registerPerson{}).
		Use(translation.Middleware([]string{"pt-BR", "en"}, "pt-BR")).
		Register(peopleModule{})
	if wire != nil {
		wire(app)
	}
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return app
}

func postJSON(body string, header map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://example.test/people", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return req
}

func problemErrors(t *testing.T, rec *httptest.ResponseRecorder) map[string][]string {
	t.Helper()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want problem+json", ct)
	}
	var problem struct {
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the 422 is not a JSON document: %v: %s", err, rec.Body)
	}
	return problem.Errors
}

// TestAJSONRequestThatFailsItsRulesIsA422InPortuguese: the application wires a
// translator and one provider, and a controller that only binds answers, per
// field, what is wrong in the language of the person, with the field's name in
// the sentence.
func TestAJSONRequestThatFailsItsRulesIsA422InPortuguese(t *testing.T) {
	rec := httptest.NewRecorder()
	peopleApp(t, nil).Handler().ServeHTTP(rec, postJSON(`{"cpf":"","cep":"abc","celular":"123"}`, nil))

	errs := problemErrors(t, rec)
	want := map[string]string{
		"cpf":     "O campo CPF é obrigatório.",
		"cep":     "O campo cep não tem o formato de um CEP.",
		"celular": "O campo celular deve ser um celular com DDD.",
	}
	for field, sentence := range want {
		if got := errs[field]; len(got) != 1 || got[0] != sentence {
			t.Errorf("errors[%s] = %v, want %q", field, got, sentence)
		}
	}
}

// TestAFormThatFailsItsRulesGoesBackWithTheMessagesAndWhatWasTyped: the same
// controller, asked by a page, redirects back, and the flash it leaves carries
// the sentences and the old input.
func TestAFormThatFailsItsRulesGoesBackWithTheMessagesAndWhatWasTyped(t *testing.T) {
	handler := peopleApp(t, nil).Handler()

	form := url.Values{"cpf": {"123"}, "cep": {"50000000"}, "note": {"typed"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.test/people", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/people/new")
	post.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/people/new" {
		t.Fatalf("answered %d to %q, want a 303 back to the form", rec.Code, rec.Header().Get("Location"))
	}
	var flash *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == security.FlashCookieName {
			flash = c
		}
	}
	if flash == nil {
		t.Fatal("no flash cookie: the form came back with nothing on it")
	}

	key := testConfig(config.EnvProd).App.Key
	next := httptest.NewRequest(http.MethodGet, "/people/new", nil)
	next.Header.Set("Accept", "text/html")
	next.AddCookie(flash)
	errs, old, ok := security.NewFlash(key, false).Take(httptest.NewRecorder(), next)
	if !ok {
		t.Fatal("the flash could not be read back")
	}
	if got := errs["cpf"]; len(got) != 1 || got[0] != "O campo CPF não é um CPF válido." {
		t.Errorf("errors[cpf] = %v", got)
	}
	if len(old["note"]) != 1 || old["note"][0] != "typed" || old["cep"][0] != "50000000" {
		t.Errorf("old input = %v", old)
	}
}

// TestSuccessReturnsOnlyTheValidatedFields: what the rules declared and passed
// comes back through Validated, and the field nobody declared does not.
func TestSuccessReturnsOnlyTheValidatedFields(t *testing.T) {
	rec := httptest.NewRecorder()
	peopleApp(t, nil).Handler().ServeHTTP(rec, postJSON(`{"cpf":"12345678901","cep":"50000000","is_admin":true}`, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Validated map[string]any
		Bound     map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, leaked := out.Validated["is_admin"]; leaked {
		t.Errorf("the field nobody declared was returned: %v", out.Validated)
	}
	if out.Validated["cpf"] != "12345678901" || out.Validated["cep"] != "50000000" || out.Bound["CPF"] != "12345678901" {
		t.Errorf("validated = %v, bound = %v", out.Validated, out.Bound)
	}
}

// TestTheLocaleOfTheRequestChoosesTheLanguage: one Factory answers in the
// language the request negotiated.
func TestTheLocaleOfTheRequestChoosesTheLanguage(t *testing.T) {
	handler := peopleApp(t, nil).Handler()
	for _, c := range []struct{ accept, want string }{
		{"en", "The CPF field is required."},
		{"pt-BR", "O campo CPF é obrigatório."},
		{"", "O campo CPF é obrigatório."},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, postJSON(`{"cpf":""}`, map[string]string{"Accept-Language": c.accept}))
		if got := problemErrors(t, rec)["cpf"]; len(got) == 0 || got[0] != c.want {
			t.Errorf("Accept-Language %q: errors[cpf] = %v, want %q", c.accept, got, c.want)
		}
	}
}

// TestARuleNobodyDeclaredFailsTheBootNamingTheRuleAndTheRequest: a typo in a
// Rules is a failure of the boot, not of the first person to send the form.
func TestARuleNobodyDeclaredFailsTheBootNamingTheRuleAndTheRequest(t *testing.T) {
	app := foundation.New(testConfig(config.EnvProd)).
		WithTranslator(portuguese()).
		WithValidation(brRules{}).
		WithValidationRequests(registerPerson{}, typoRequest{}).
		Register(peopleModule{})

	err := app.Boot(context.Background())
	if err == nil {
		t.Fatal("Boot passed although a request names a rule nothing declares")
	}
	for _, name := range []string{"cpff", "typoRequest"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %q: %v", name, err)
		}
	}

	// The provider is what makes the rule exist: without it the same request
	// that booted above does not.
	missing := foundation.New(testConfig(config.EnvProd)).WithValidationRequests(registerPerson{}).Register(peopleModule{})
	if err := missing.Boot(context.Background()); err == nil || !strings.Contains(err.Error(), "cpf") {
		t.Errorf("Boot without the provider = %v, want the rule cpf named", err)
	}
}

// TestTheBuilderWiresValidationLikeBootstrapApp: the shape bootstrap/app.go is
// written in, five lines and no registry.
func TestTheBuilderWiresValidationLikeBootstrapApp(t *testing.T) {
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithTranslator(portuguese()).
			WithValidation(brRules{}).
			WithValidationRequests(registerPerson{}).
			WithMiddleware(func(m *foundation.Middleware) { m.ValidateCSRFTokensExcept("/people") }).
			WithProviders(peopleModule{})
	})
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, postJSON(`{"cpf":"1"}`, nil))
	if got := problemErrors(t, rec)["cpf"]; len(got) != 1 || got[0] != "O campo CPF não é um CPF válido." {
		t.Errorf("errors[cpf] = %v", got)
	}

	_, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithValidationRequests(typoRequest{}).WithProviders(peopleModule{})
	})
	if err != nil {
		t.Fatalf("Create = %v: the rules are checked when the application boots, not when it is composed", err)
	}
}
