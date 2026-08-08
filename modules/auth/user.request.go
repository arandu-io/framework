package auth

import (
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// CreateUserRequest is the input contract. Fields are explicit: there is no mass
// assignment, so a request body cannot write a column nobody meant to expose --
// the bug class does not exist here rather than being contained.
type CreateUserRequest struct {
	Email    string
	Password string
	Roles    []string
}

// Validate reports the errors per field.
func (r CreateUserRequest) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "email", r.Email)
	validation.Email(e, "email", r.Email)
	validation.MaxLen(e, "email", r.Email, 254)
	validation.MinLen(e, "password", r.Password, security.MinPasswordLen)
	// The upper bound is not cosmetic: argon2 cost grows with input length, so an
	// unbounded password field is a denial of service vector.
	validation.MaxLen(e, "password", r.Password, 128)
	return e
}

// LoginRequest is the login input. It does not check the password length: the
// rule that applies here is whether the credentials match, and a length message
// on login would leak the policy of existing accounts.
type LoginRequest struct {
	Email    string
	Password string
}

// Validate reports the errors per field.
func (r LoginRequest) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "email", r.Email)
	validation.Required(e, "password", r.Password)
	return e
}

// Compile-time proof that both requests honor the validation contract.
var (
	_ validation.Validatable = CreateUserRequest{}
	_ validation.Validatable = LoginRequest{}
)
