// The compiled rule set and what passed it, answered by
// github.com/arandu-io/hesape/validation.
//
// Both names survived the move whole, signature included: Set.Validate still
// takes url.Values and still answers (Input, Errors). Input gained two readers
// there -- Data and File, for the nested value and the upload url.Values cannot
// hold -- and gaining a method is not a break for anybody written against the
// eight that were already here.

package validation

import hvalidation "github.com/arandu-io/hesape/validation"

// Set is a compiled, checked rule set.
//
// Build one in a package-level variable with MustCompile: the rules are then
// parsed once, at boot, and a set that boots is a set whose names are all real.
// A Set is read-only once compiled, so one is shared by every request.
type Set = hvalidation.Set

// Input is what passed the rules. It is the only way to read a submitted value
// out of a validated request: a field the set does not declare is not in here,
// so a value nobody wrote a rule for cannot reach a repository by accident.
type Input = hvalidation.Input
