package foundation

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

// deadlineProbe records how much time its readiness check was given.
type deadlineProbe struct {
	budget  time.Duration
	bounded bool
}

func (*deadlineProbe) Name() string { return "probe" }

func (*deadlineProbe) Routes(*fhttp.Router) {}

func (p *deadlineProbe) Health(ctx context.Context) error {
	var deadline time.Time
	deadline, p.bounded = ctx.Deadline()
	p.budget = time.Until(deadline)
	return nil
}

// TestTheReadinessCheckIsGivenADeadline pins the bound at the only place a
// module can observe it: the context its check receives.
//
// A check called with the request context alone is bounded by nothing this
// framework decides. The dependency decides instead, and a dependency that
// stopped answering decides to take forever -- so the caller's probe gives up
// first and records an unanswered request, which reads as "the process is gone"
// rather than "one dependency is". The two call for opposite actions.
func TestTheReadinessCheckIsGivenADeadline(t *testing.T) {
	probe := &deadlineProbe{}
	a := New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: ":0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}).Register(probe)
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	a.Handler().ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, internalPrefix+"health", nil))

	if !probe.bounded {
		t.Fatal("the check ran with no deadline: the endpoint waits for as long as the dependency does")
	}
	if probe.budget > readinessTimeout {
		t.Errorf("the check was given %s, more than readinessTimeout of %s", probe.budget, readinessTimeout)
	}
	if probe.budget <= 0 {
		t.Errorf("the check was given %s, so it was over before it began", probe.budget)
	}
}

// TestReadinessAnswersBeforeTheServerCutsItOff: writeTimeout is a deadline on
// the whole response, so a readiness bound at or above it never fires first. The
// server would cut the connection instead, and a cut connection is the
// unanswered request the bound exists to replace with a 503 that names a module.
func TestReadinessAnswersBeforeTheServerCutsItOff(t *testing.T) {
	if readinessTimeout >= writeTimeout {
		t.Fatalf("readinessTimeout = %s, writeTimeout = %s: the server ends the response before the check gives up",
			readinessTimeout, writeTimeout)
	}
}
