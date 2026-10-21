// What Create collects from the registered modules, so that a module which
// brings its own CSRF exemptions or job handlers costs the application no line.

package foundation

import (
	"fmt"

	"github.com/arandu-io/framework/http/middleware"
	hfoundation "github.com/arandu-io/hesape/foundation"
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
type CSRFExempt = hfoundation.CSRFExempt

// moduleCSRFExemptions collects the paths every CSRFExempt module declares, in
// registration order, each once.
//
// Two modules declaring one path is not refused: an exemption is a set, and
// the second declaration exempts nothing the first did not.
func moduleCSRFExemptions(mods []Module) ([]string, error) {
	var out []string
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
				out = append(out, p)
			}
		}
	}
	return out, nil
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
