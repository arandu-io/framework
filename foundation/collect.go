// What Create collects from the registered modules, so that a module which
// brings its own CSRF exemptions or job handlers costs the application no line.

package foundation

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/arandu-io/framework/http/middleware"
	hfoundation "github.com/arandu-io/hesape/foundation"
	"github.com/arandu-io/hesape/queue"
)

// CSRFExempt is optional: the module answers requests that cannot carry a
// CSRF token -- a webhook receiver, called by another system with no session --
// and declares the paths they arrive under. Create exempts them from the CSRF
// check beside what Middleware.ValidateCSRFTokensExcept names, read the same
// way: an entry ending in '/' covers every path under it, one without covers
// exactly that path.
//
// An entry the check refuses -- empty, "/", not beginning with '/', not a clean
// path -- stops Create naming the module, rather than turning the check off.
// An entry that also covers a write route the module did not register stops
// Boot naming the module and the route. Boot writes every entry to the log.
type CSRFExempt = hfoundation.CSRFExempt

// csrfExemption is one path a CSRFExempt module declares, with the name of the
// module that declared it.
type csrfExemption struct {
	module string
	path   string
}

// moduleCSRFExemptions collects the paths every CSRFExempt module declares, in
// registration order, each once.
//
// Two modules declaring one path is not refused: an exemption is a set, and
// the second declaration exempts nothing the first did not. The first
// declarer is the one named.
func moduleCSRFExemptions(mods []Module) ([]csrfExemption, error) {
	var out []csrfExemption
	seen := map[string]bool{}
	for _, m := range mods {
		exempt, ok := capable[CSRFExempt](m)
		if !ok {
			continue
		}
		for _, p := range exempt.CSRFExemptPaths() {
			if err := checkCSRFExemption(p); err != nil {
				return nil, fmt.Errorf("arandu: the module %q declares a CSRF exemption the check refuses: %w", m.Name(), err)
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, csrfExemption{module: m.Name(), path: p})
			}
		}
	}
	return out, nil
}

// exemptionPaths is the paths of exempt, in order.
func exemptionPaths(exempt []csrfExemption) []string {
	out := make([]string, 0, len(exempt))
	for _, e := range exempt {
		out = append(out, e.path)
	}
	return out
}

// validateCSRFExemptions writes every exemption the modules declared to the
// log, and refuses one that covers a route its module did not register.
//
// A module's exemption is meant for the requests its own routes answer -- the
// webhook a provider calls with no session. One that also covers a route of
// the application or of another module turns the CSRF check off for that
// route without a line in the application saying so, which is why it stops
// the boot naming both. A route that only reads -- GET, HEAD, OPTIONS -- is
// not checked by the CSRF middleware, and is left out.
func (a *Application) validateCSRFExemptions() error {
	for _, e := range a.csrfExempt {
		a.log.Info("csrf exemption", "module", e.module, "path", e.path)
	}
	for _, e := range a.csrfExempt {
		for _, route := range a.router.Routes() {
			if route.Module == e.module || csrfSafeMethod(route.Method) {
				continue
			}
			for _, pattern := range muxPatterns(route.Pattern) {
				if csrfCovers(e.path, pattern) {
					return fmt.Errorf("arandu: the module %q declares the CSRF exemption %q, which covers the route %s %q of the module %q; "+
						"a module exempts only the routes it registers", e.module, e.path, route.Method, route.Pattern, route.Module)
				}
			}
		}
	}
	return nil
}

// csrfSafeMethod reports whether the CSRF middleware lets method through
// without a token.
func csrfSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// csrfCovers reports whether the exemption, read as CSRFExcept reads it,
// takes some path the route pattern answers.
//
// A pattern answers paths of one length, or -- ending in '/' or in a
// {name...} segment -- of any length past its fixed segments. A parameter on
// either side matches any segment, and a fixed segment matches only itself.
func csrfCovers(exemption, pattern string) bool {
	subtree := strings.HasSuffix(exemption, "/")
	want := strings.Split(strings.TrimSuffix(exemption[1:], "/"), "/")

	var route []string
	open := false
	segments := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
scan:
	for i, s := range segments {
		switch {
		case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "...}"):
			open = true
		case s == "{$}":
			route = append(route, "")
		case s == "" && i == len(segments)-1:
			open = true
		default:
			route = append(route, s)
			continue
		}
		break scan
	}

	for i := 0; i < len(route) && i < len(want); i++ {
		r, w := route[i], want[i]
		if strings.HasPrefix(w, "{") || strings.HasPrefix(r, "{") {
			continue
		}
		if r != w {
			return false
		}
	}

	switch {
	case subtree && open:
		return true
	case subtree:
		return len(route) > len(want)
	case open:
		return len(want) > len(route)
	default:
		return len(route) == len(want)
	}
}

// checkCSRFExemption asks the CSRF option itself whether it takes p, so a
// module's path is held to exactly the rule the application's are. The option
// refuses by panicking, which is right for a path written in bootstrap and
// wrong for one a module answers: that one becomes the error Create returns.
func checkCSRFExemption(p string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	middleware.CSRFExcept(p)
	return nil
}

// JobHandlers is optional: the module runs jobs of its own and registers their
// handlers on the worker queue:work starts. Create collects them from every
// provider beside what WithJobs registers, so a module that brings its own
// background work needs no line in the application.
//
// A job name given a handler by two declarers -- two modules, or a module and
// the application -- stops Create naming both, because a job runs one handler
// and which one won would otherwise be the order the registrations happened
// to run in.
type JobHandlers = queue.JobHandlers

// jobSource is one declarer of job handlers and how it registers them.
type jobSource struct {
	by       string
	register func(*queue.Worker) error
}

// registerJobs proves every registration -- the application's WithJobs and
// every JobHandlers provider's -- on a worker of its own that is dropped, and
// answers the registration queue:work runs, or nil when nobody registers
// anything, which is what makes queue:work refuse to start a worker with
// nothing to run.
//
// Each declarer is proven alone so that a name two of them register is found
// with both names in hand, rather than as the panic the second Handle would
// raise on a shared worker.
func registerJobs(d *Drivers, app []func(*queue.Worker) error, mods []Module) (func(*queue.Worker), error) {
	var sources []jobSource
	if len(app) > 0 {
		sources = append(sources, jobSource{by: "the application", register: func(w *queue.Worker) error {
			for _, register := range app {
				if err := register(w); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	for _, m := range mods {
		if handlers, ok := capable[JobHandlers](m); ok {
			sources = append(sources, jobSource{by: fmt.Sprintf("the module %q", m.Name()), register: handlers.RegisterJobHandlers})
		}
	}
	if len(sources) == 0 {
		return nil, nil
	}

	owner := map[string]string{}
	for _, s := range sources {
		probe := queue.NewWorker(d.Queue, queue.WorkerOptions{})
		if err := registerOn(probe, s.register); err != nil {
			return nil, fmt.Errorf("arandu: registering the job handlers of %s: %w", s.by, err)
		}
		names := probe.Names()
		slices.Sort(names)
		for _, name := range names {
			if first, taken := owner[name]; taken {
				return nil, fmt.Errorf("arandu: the job %q is given a handler by %s and by %s, and a job runs one handler", name, first, s.by)
			}
			owner[name] = s.by
		}
	}

	// Proven above, so what the worker is handed cannot fail: the same
	// functions over the same services, with no name registered twice.
	return func(w *queue.Worker) {
		for _, s := range sources {
			_ = s.register(w)
		}
	}, nil
}

// registerOn runs one declarer's registration, turning the panic Handle raises
// for a name the declarer registered twice into an error. Any other panic is a
// defect in the registration and is left to propagate with its stack.
func registerOn(w *queue.Worker, register func(*queue.Worker) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(string)
			if !ok || !strings.HasPrefix(msg, "queue: ") || !strings.HasSuffix(msg, " already has a handler") {
				panic(r)
			}
			err = errors.New(msg)
		}
	}()
	return register(w)
}
