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

// RegisterRequest is what a self-registration form sends.
//
// It has no Roles field, and that is the whole difference from
// CreateUserRequest. A registration form that carried roles would be a
// registration form that could ask for "admin" -- and the only thing between the
// request and the column would be the handler remembering to drop it.
//
// The policy refuses it a second time, on the candidate rather than on the
// request. Two answers to the same question, because this one is the one that
// still holds after somebody adds a field here.
type RegisterRequest struct {
	Name  string
	Email string

	// Password and PasswordConfirmation are the two boxes of the form. Both are
	// here rather than compared in the handler, so the rule is in the same place
	// as the length rule and is tested with it.
	Password             string
	PasswordConfirmation string
}

// Validate reports the errors per field.
func (r RegisterRequest) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "name", r.Name)
	validation.MaxLen(e, "name", r.Name, 80)
	validation.Required(e, "email", r.Email)
	validation.Email(e, "email", r.Email)
	validation.MaxLen(e, "email", r.Email, 254)
	validation.MinLen(e, "password", r.Password, security.MinPasswordLen)
	validation.MaxLen(e, "password", r.Password, 128)
	validation.Confirmed(e, "password_confirmation", r.Password, r.PasswordConfirmation)
	return e
}

// Compile-time proof that the requests honor the validation contract.
var (
	_ validation.Validatable = CreateUserRequest{}
	_ validation.Validatable = LoginRequest{}
	_ validation.Validatable = RegisterRequest{}
)
