package pwd

import (
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/setting"
)

// Validate enforces F1 complexity and bcrypt's maximum byte length.
func Validate(password string) error {
	return ValidatePolicy(password, setting.DefaultSecurity())
}

// ValidatePolicy applies the live tenant complexity policy within bcrypt's byte limit.
func ValidatePolicy(password string, policy setting.Security) error {
	letter, digit := false, false
	for _, r := range password {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < policy.PasswordMinLength || len(password) > 72 || (policy.PasswordRequireMixed && (!letter || !digit)) {
		return errors.New("password does not satisfy the tenant complexity policy or bcrypt byte limit")
	}
	return nil
}
