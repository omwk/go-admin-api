package pwd

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

// Validate enforces F1 complexity and bcrypt's maximum byte length.
func Validate(password string) error {
	letter, digit := false, false
	for _, r := range password {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || len(password) > 72 || !letter || !digit {
		return errors.New("password must contain 8 or more characters with letters and digits, within 72 bytes")
	}
	return nil
}
