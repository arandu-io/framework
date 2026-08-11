package validation

// The `timezone` rule is time.LoadLocation, which reads the zone database the
// platform ships. A scratch container ships none, so without the embedded copy
// the rule answers "is not a valid time zone" for every zone on earth -- on
// exactly the deployment nobody tests, and only there.
//
// The cost is about 450 KB in every binary that links this package, and this
// package is reached from httpx and view, so that is every binary. It is paid
// deliberately: a rule that passes in development and rejects everything in
// production is worse than a bigger binary, and the alternative -- an
// instruction in a guide to install tzdata in the image -- is a rule that works
// only for the people who read the guide.
import _ "time/tzdata"
