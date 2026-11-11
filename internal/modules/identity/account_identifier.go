package identity

import (
	"errors"
	"net/mail"
	"strings"
)

// AccountIdentifier returns a fixed database column and normalized account value.
// Unprefixed local mobile numbers use the application's zh-CN default region.
func AccountIdentifier(account string) (string, string, error) {
	value := strings.TrimSpace(account)
	if value == "" || len(value) > 256 {
		return "", "", invalidAdmin()
	}
	if strings.Contains(value, "@") {
		address, err := mail.ParseAddress(value)
		if err != nil || address.Address != value {
			return "", "", invalidAdmin()
		}
		return "email", strings.ToLower(value), nil
	}
	if strings.HasPrefix(value, "+") || digitsOnly(value) {
		phone, err := NormalizePhone(value)
		return "phone", phone, err
	}
	return "username", value, nil
}

// NormalizePhone accepts E.164 and mainland local mobile formatting.
func NormalizePhone(value string) (string, error) {
	value = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')':
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if strings.HasPrefix(value, "00") {
		value = "+" + value[2:]
	}
	if !strings.HasPrefix(value, "+") {
		if len(value) != 11 || value[0] != '1' || !digitsOnly(value) {
			return "", errors.New("invalid phone format")
		}
		value = "+86" + value
	}
	number := strings.TrimPrefix(value, "+")
	if len(number) < 8 || len(number) > 15 || number[0] == '0' || !digitsOnly(number) {
		return "", errors.New("invalid phone format")
	}
	return value, nil
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
