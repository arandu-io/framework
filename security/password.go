// Password hashing, answered by github.com/arandu-io/hesape/hashing.
//
// The three functions were renamed on the way there -- Make, Check and
// NeedsRehash -- so these three are one-line calls through. The argon2id
// parameters are the same numbers on both sides, so a hash written by the old
// code verifies against the new one.

package security

import "github.com/arandu-io/hesape/hashing"

// MinPasswordLen is the shortest password the framework accepts. Length beats
// composition rules: it is the only parameter that reliably raises the cost of
// an offline attack.
const MinPasswordLen = hashing.MinPasswordLen

// ErrInvalidPassword means the password does not match the stored hash.
var ErrInvalidPassword = hashing.ErrInvalidPassword

// HashPassword returns the hash in PHC string format, which is self-describing
// and therefore allows rehashing when parameters change.
//
// Renamed on the way to hesape: it is hashing.Make there.
func HashPassword(plain string) (string, error) { return hashing.Make(plain) }

// VerifyPassword compares in constant time.
//
// Renamed on the way to hesape: it is hashing.Check there, which also reads
// back argon2i and bcrypt -- what an imported users table holds.
func VerifyPassword(plain, encoded string) error { return hashing.Check(plain, encoded) }

// NeedsRehash reports that the hash was produced with older parameters, so the
// caller should rehash on the next successful login.
func NeedsRehash(encoded string) bool { return hashing.NeedsRehash(encoded) }
