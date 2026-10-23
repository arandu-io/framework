package feature

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/arandu-io/framework/test"
)

// app answers one path per thing the assertions read: a page, an API answer,
// a download, a sign-out and an empty success.
func app() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "req-1")
		_, _ = w.Write([]byte(`<ul><li>Ana &amp; Bia</li><!-- <b>draft</b> --><li>Caio</li></ul>`))
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":[{"id":"1","name":"Ana"},{"id":"2","name":"Bia"}],"meta":{"total":2}}`))
	})
	mux.HandleFunc("/export", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="notes.csv"`)
		_, _ = w.Write([]byte("id\n1\n"))
	})
	mux.HandleFunc("/signout", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
		http.SetCookie(w, &http.Cookie{Name: "theme", Value: "dark", Path: "/"})
		w.Header().Set("Location", "/login?from=signout")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// TestTheAssertionsPassOnWhatTheyDescribe: every assertion brought over from
// the response of hesape/testing, against a response that satisfies it.
func TestTheAssertionsPassOnWhatTheyDescribe(t *testing.T) {
	c := test.NewClient(t, app())

	c.Get("/page").AssertOk().AssertSuccessful().
		AssertHeader("X-Request-Id", "req-1").AssertHeaderMissing("Location").
		AssertSeeInOrder("Ana", "Caio").
		AssertSeeText("Ana & Bia").AssertSeeTextInOrder("Ana & Bia", "Caio").
		AssertDontSeeText("draft").AssertCookieMissing("session")

	c.Get("/api").AssertCreated().
		AssertJSON(map[string]any{"meta": map[string]any{"total": 2}}).
		AssertJSONPath("data.1.name", "Bia").AssertJSONMissingPath("data.2").
		AssertJSONFragment(map[string]any{"name": "Ana"}).
		AssertJSONMissing(map[string]any{"password": "secret"}).
		AssertJSONStructure(map[string]any{"data": map[string]any{"*": []any{"id", "name"}}, "meta": []any{"total"}}).
		AssertJSONCount(2, "data")

	c.Get("/export").AssertDownload("notes.csv").AssertContent("id\n1\n")

	c.Get("/signout").AssertFound().AssertRedirectContains("/login").
		AssertCookieExpired("session").AssertCookie("theme", "dark")

	c.Get("/empty").AssertNoContent()
	c.Get("/missing").AssertNotFound()
}

// failingCase is the environment variable a child process reads to know which
// assertion it is meant to fail.
const failingCase = "ARANDUTEST_FAILING_CASE"

// failing is each assertion against a response that contradicts it, with the
// words its failure message has to carry.
var failing = map[string]struct {
	run  func(c *test.Client)
	want string
}{
	"see":            {func(c *test.Client) { c.Get("/page").AssertSee("refunded") }, `does not show "refunded"`},
	"dont see text":  {func(c *test.Client) { c.Get("/page").AssertDontSeeText("Ana & Bia") }, `reads "Ana & Bia"`},
	"see in order":   {func(c *test.Client) { c.Get("/page").AssertSeeInOrder("Caio", "Ana") }, `does not show "Ana"`},
	"json missing":   {func(c *test.Client) { c.Get("/api").AssertJSONMissing(map[string]any{"name": "Ana"}) }, ""},
	"json path":      {func(c *test.Client) { c.Get("/api").AssertJSONPath("data.0.name", "Bia") }, ""},
	"cookie expired": {func(c *test.Client) { c.Get("/signout").AssertCookieExpired("theme") }, "without expiring it"},
	"successful":     {func(c *test.Client) { c.Get("/missing").AssertSuccessful() }, "want a successful status"},
	"header":         {func(c *test.Client) { c.Get("/page").AssertHeader("X-Request-Id", "req-2") }, `want "req-2"`},
	"download":       {func(c *test.Client) { c.Get("/export").AssertDownload("other.csv") }, `want the file "other.csv"`},
	"no content":     {func(c *test.Client) { c.Get("/page").AssertNoContent() }, "want 204"},
	"not json":       {func(c *test.Client) { c.Get("/page").AssertJSONCount(1) }, "did not answer JSON"},
}

// TestTheFailingCaseRunsInAChild is the body the child process runs. Outside
// one it does nothing.
func TestTheFailingCaseRunsInAChild(t *testing.T) {
	name := os.Getenv(failingCase)
	if name == "" {
		t.Skip("runs only as the child of TestEveryAssertionFailsOnWhatContradictsIt")
	}
	failing[name].run(test.NewClient(t, app()))
}

// TestEveryAssertionFailsOnWhatContradictsIt: an assertion that never fails
// proves nothing. Each case runs in a child process, because a failing *testing.T
// cannot be observed from the test that owns it, and the child has to exit
// non-zero with the failure message.
func TestEveryAssertionFailsOnWhatContradictsIt(t *testing.T) {
	if os.Getenv(failingCase) != "" {
		t.Skip("the child runs one case, not the table")
	}
	for name, c := range failing {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestTheFailingCaseRunsInAChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), failingCase+"="+name)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the assertion passed on a response that contradicts it:\n%s", out)
			}
			if !strings.Contains(string(out), "--- FAIL") || !strings.Contains(string(out), c.want) {
				t.Fatalf("the child failed without the message %q:\n%s", c.want, out)
			}
		})
	}
}
