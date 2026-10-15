package feature

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	fwebhook "github.com/arandu-io/framework/webhook"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
	"github.com/arandu-io/hesape/exception"
	"github.com/arandu-io/hesape/pipeline"
	"github.com/arandu-io/hesape/routing"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// signedIn is a guard that admits every request as the subject, so a
// management route is answered by its policy on both routers alike.
func signedIn(subject security.Subject) pipeline.Middleware[http.Handler] {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithSubject(r.Context(), subject)))
		})
	}
}

// twoRouters is one native module registered twice: through the bridge on the
// Framework router, and through its own Routes on routing.Router.
type twoRouters struct {
	native    *hwebhook.Module
	framework *fhttp.Router
	hesape    *routing.Router
}

// bothWays builds the module with every kind of receiver and a guard that
// signs in a subject the closed policy refuses.
func bothWays(t *testing.T) twoRouters {
	t.Helper()

	quiet := billing()
	quiet.Name, quiet.RefusalBody = "quiet", hwebhook.RefusalEmpty
	native, err := hwebhook.New(hwebhook.Config{Key: []byte(appKey), Receivers: []hwebhook.Receiver{
		billing(), quiet,
		{
			Name: "cielo", Path: "cielo/{token}", Resolve: cieloResolver,
			Validator: hwebhook.HeaderSecret{Header: "X-Perata-Notification"}, MaxBodyBytes: 64 << 10,
			ProcessSync: &filing{}, Statuses: hwebhook.Statuses{EmptyBody: http.StatusBadRequest},
		},
	}}, noDatabase(), signedIn(administrator()))
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}

	framework := fhttp.NewRouter().WithFlash(security.NewFlash([]byte(appKey), false))
	module := fwebhook.NewModule(native)
	module.Routes(framework.ForModule(module.Name()))

	hesape := routing.NewRouter()
	native.Routes(hesape.ForModule(native.Name()))
	return twoRouters{native: native, framework: framework, hesape: hesape}
}

// TestTheBridgeAnswersEveryRefusalAsTheNativeRouterDoes sends the same request
// to the module registered both ways and requires the same answer: the status,
// the content type and the body, which for a client that wants JSON is the
// problem document. The two adapters are separate code reading the same status
// table, and this is what keeps them reading it the same way.
func TestTheBridgeAnswersEveryRefusalAsTheNativeRouterDoes(t *testing.T) {
	t.Parallel()

	both := bothWays(t)
	cielo := "/webhooks/cielo/" + token("acme", "ep-1")
	jsonHeaders := map[string]string{"Accept": "application/json", "Content-Type": "application/json"}

	for _, tc := range []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		status  int
		problem bool
	}{
		{"a policy refusal to a page", http.MethodGet, "/webhook/endpoints", "", nil, http.StatusForbidden, false},
		{"a policy refusal wanting JSON", http.MethodGet, "/webhook/endpoints", "", jsonHeaders, http.StatusForbidden, true},
		{"a refused write wanting JSON", http.MethodPost, "/webhook/endpoints/e-1/rotate", "", jsonHeaders, http.StatusForbidden, true},
		{"a rejected input wanting JSON", http.MethodPost, "/webhook/endpoints", `{"name":""}`, jsonHeaders, http.StatusUnprocessableEntity, true},
		{"a bad signature", http.MethodPost, "/webhooks/billing", `{"a":1}`, map[string]string{"Signature": "00"}, http.StatusForbidden, false},
		{"a bad signature wanting JSON", http.MethodPost, "/webhooks/billing", `{"a":1}`, map[string]string{"Signature": "00", "Accept": "application/json"}, http.StatusForbidden, true},
		{"a bad signature on the second method", http.MethodPut, "/webhooks/billing", `{"a":1}`, map[string]string{"Signature": "00"}, http.StatusForbidden, false},
		{"an empty refusal", http.MethodPost, "/webhooks/quiet", `{"a":1}`, map[string]string{"Signature": "00"}, http.StatusForbidden, false},
		{"a record the resolver does not know", http.MethodPost, "/webhooks/cielo/" + token("acme", "ep-2"), `{"a":1}`, map[string]string{"X-Perata-Notification": cieloSecret}, http.StatusNotFound, false},
		{"a resolver that is down", http.MethodPost, "/webhooks/cielo/down.x.y", `{"a":1}`, map[string]string{"X-Perata-Notification": cieloSecret, "Accept": "application/json"}, http.StatusServiceUnavailable, true},
		{"a body above the limit", http.MethodPost, cielo, strings.Repeat("x", 64<<10+1), map[string]string{"X-Perata-Notification": cieloSecret}, http.StatusRequestEntityTooLarge, false},
		{"an empty body", http.MethodPost, cielo, "", map[string]string{"X-Perata-Notification": cieloSecret}, http.StatusBadRequest, false},
	} {
		request := func() *http.Request {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			for name, value := range tc.headers {
				req.Header.Set(name, value)
			}
			return req
		}
		got, want := serve(t, both.framework, request()), serve(t, both.hesape, request())

		if got.Code != tc.status || want.Code != tc.status {
			t.Errorf("%s: the bridge answered %d and the native router %d, want %d", tc.name, got.Code, want.Code, tc.status)
			continue
		}
		if got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
			t.Errorf("%s: the bridge answered %q and the native router %q", tc.name, got.Header().Get("Content-Type"), want.Header().Get("Content-Type"))
		}
		if got.Body.String() != want.Body.String() {
			t.Errorf("%s: the bridge answered\n%s\nand the native router\n%s", tc.name, got.Body.String(), want.Body.String())
		}
		if tc.problem {
			var problem exception.Problem
			if got.Header().Get("Content-Type") != exception.ProblemContentType || json.Unmarshal(got.Body.Bytes(), &problem) != nil || problem.Status != tc.status {
				t.Errorf("%s: the bridge answered %q %s, want a %d problem", tc.name, got.Header().Get("Content-Type"), got.Body.String(), tc.status)
			}
		}
	}
}

// TestAPageThatFailedValidationIsTheOneAnswerThatDiffers holds the divergence
// the bridge exists for. A form that failed validation has a page to go back
// to on the Framework router, which carries a flash; the native router carries
// none and answers every client as an API.
func TestAPageThatFailedValidationIsTheOneAnswerThatDiffers(t *testing.T) {
	t.Parallel()

	both := bothWays(t)
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhook/endpoints", strings.NewReader("name="))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}

	if rec := serve(t, both.framework, request()); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("the bridge answered a rejected form %d to %q, want %d back to /", rec.Code, rec.Header().Get("Location"), http.StatusSeeOther)
	}
	if rec := serve(t, both.hesape, request()); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the native router answered a rejected form %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

// TestTheRouteNamesAreTheNativeAndThePublishedOnes reads the route table the
// bridge fills, the one route introspection prints, and requires every row to
// be the native action at the same position and, for the management routes
// and the receivers, the method, pattern and name the separate webhook module
// published.
func TestTheRouteNamesAreTheNativeAndThePublishedOnes(t *testing.T) {
	t.Parallel()

	both := bothWays(t)
	actions := both.native.HTTPActions()
	bridged := both.framework.Routes()
	native := both.hesape.Routes()
	if len(bridged) != len(actions) || len(native) != len(actions) {
		t.Fatalf("the bridge registered %d routes and the native router %d, for %d actions", len(bridged), len(native), len(actions))
	}

	published := append([]publishedRoute{
		{http.MethodPost, "/webhooks/billing", "webhook.receive.billing"},
		{http.MethodPut, "/webhooks/billing", "webhook.receive.billing.put"},
		{http.MethodPost, "/webhooks/quiet", "webhook.receive.quiet"},
		{http.MethodPut, "/webhooks/quiet", "webhook.receive.quiet.put"},
		{http.MethodPost, "/webhooks/cielo/{token}", "webhook.receive.cielo"},
	}, publishedManagementRoutes...)
	if len(published) != len(actions) {
		t.Fatalf("the module lists %d actions, and %d routes were published", len(actions), len(published))
	}

	for i, action := range actions {
		row := func(r *fhttp.Route) publishedRoute { return publishedRoute{r.Method, r.Pattern, r.RouteName()} }
		want := publishedRoute{action.Method, action.Path, action.Name}
		if got := row(bridged[i]); got != want || bridged[i].Module != "webhook" {
			t.Errorf("bridged route %d = %v in %q, want the native action %v in webhook", i, got, bridged[i].Module, want)
		}
		if got := row(native[i]); got != want {
			t.Errorf("native route %d = %v, want %v", i, got, want)
		}
		if want != published[i] {
			t.Errorf("action %d = %v, and the separate module published %v", i, want, published[i])
		}
	}
}

// application is an Application with the module registered through the bridge
// and, when csrf is true, the CSRF check an application installs, exempting
// the receive paths the native module names.
func application(t *testing.T, csrf bool) (*foundation.Application, *hwebhook.Module) {
	t.Helper()

	sessions := newSessions()
	lpr := billing()
	lpr.Name, lpr.Methods, lpr.Mount = "lpr", nil, "/integrations/lpr/{org}"
	native, err := hwebhook.New(hwebhook.Config{Key: []byte(appKey), Receivers: []hwebhook.Receiver{billing(), lpr}},
		noDatabase(), middleware.RequireAuth(sessions))
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}

	app := foundation.New(bootstrap.Configuration{
		App:           config.App{Name: "test", Env: config.EnvProd, HTTPAddr: ":0", Key: make([]byte, encryption.KeySize)},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}).Register(fwebhook.NewModule(native))
	if csrf {
		app.Use(middleware.CSRFProtect(security.NewCSRF([]byte(appKey), time.Hour), sessions.IDFromRequest,
			middleware.CSRFExcept(native.ReceivePaths()...)))
	}
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return app, native
}

// TestTheApplicationCollectsWhatTheModuleDeclares registers the bridge the way
// an application does and reads back what the Application collected: every
// action as a route under its name, the native migrations in their order, and
// the native cleans.
func TestTheApplicationCollectsWhatTheModuleDeclares(t *testing.T) {
	t.Parallel()

	app, native := application(t, false)

	var routes []string
	for _, route := range app.Routes() {
		if route.Module == "webhook" {
			routes = append(routes, route.Method+" "+route.Pattern+" "+route.RouteName())
		}
	}
	var actions []string
	for _, action := range native.HTTPActions() {
		actions = append(actions, action.Method+" "+action.Path+" "+action.Name)
	}
	if !slices.Equal(routes, actions) {
		t.Fatalf("the Application registered\n%v\nfor the actions\n%v", routes, actions)
	}

	var collected, declared []string
	for _, migration := range app.Migrations() {
		collected = append(collected, migration.GetName())
	}
	for _, migration := range native.Migrations() {
		declared = append(declared, migration.GetName())
	}
	if len(declared) == 0 || !slices.Equal(collected, declared) {
		t.Fatalf("the Application collected the migrations %v, and the module declares %v", collected, declared)
	}

	var tasks []string
	for _, task := range app.Tasks() {
		tasks = append(tasks, task.ID)
	}
	if want := []string{"webhook.deliveries.clean", "webhook.calls.clean"}; !slices.Equal(tasks, want) {
		t.Fatalf("the Application collected the tasks %v, want %v", tasks, want)
	}
}

// TestTheReceivePathsExemptTheReceiversFromCSRF holds the line an application
// writes in its bootstrap. A receiver is called by another system, with no
// session and no token, so the CSRF check refuses it unless its path is
// exempt; with the exemption the call reaches the receiver, whose signature
// check is what refuses a forged one. A management route stays checked.
func TestTheReceivePathsExemptTheReceiversFromCSRF(t *testing.T) {
	t.Parallel()

	call := func(target string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"a":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Signature", "00")
		return req
	}
	receivers := []string{"/webhooks/billing", "/integrations/lpr/acme"}

	unprotected, _ := application(t, false)
	for _, target := range receivers {
		if rec := serve(t, unprotected.Handler(), call(target)); rec.Code != http.StatusForbidden {
			t.Fatalf("with no CSRF check %s answered %d, want the receiver's %d", target, rec.Code, http.StatusForbidden)
		}
	}

	protected, native := application(t, true)
	if !slices.Equal(native.ReceivePaths(), []string{"/webhooks/", "/integrations/lpr/"}) {
		t.Fatalf("ReceivePaths() = %v", native.ReceivePaths())
	}
	for _, target := range receivers {
		if rec := serve(t, protected.Handler(), call(target)); rec.Code != http.StatusForbidden {
			t.Errorf("an exempt receiver %s answered %d, want the receiver's own %d", target, rec.Code, http.StatusForbidden)
		}
	}
	if rec := serve(t, protected.Handler(), call("/webhook/endpoints")); rec.Code != middleware.StatusCSRFExpired {
		t.Errorf("a management route answered %d with no token, want %d", rec.Code, middleware.StatusCSRFExpired)
	}

	// The same receivers without the exemption are refused before they are
	// reached, which is what makes the exemption the assertion above.
	sessions := newSessions()
	app := foundation.New(bootstrap.Configuration{
		App:           config.App{Name: "test", Env: config.EnvProd, HTTPAddr: ":0", Key: make([]byte, encryption.KeySize)},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}).Register(fwebhook.NewModule(native)).
		Use(middleware.CSRFProtect(security.NewCSRF([]byte(appKey), time.Hour), sessions.IDFromRequest))
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	for _, target := range receivers {
		if rec := serve(t, app.Handler(), call(target)); rec.Code != middleware.StatusCSRFExpired {
			t.Errorf("a receiver %s with no exemption answered %d, want %d", target, rec.Code, middleware.StatusCSRFExpired)
		}
	}
}
