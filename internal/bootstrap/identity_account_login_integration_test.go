package bootstrap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountLoginIntegrationIdentifiersShareFailures(t *testing.T) {
	// F1 §5.9: local phone normalization, email case and lock count all resolve one user.
	f := newAccountFixture(t)
	email := strings.ToLower(accountPassword(t)) + "@example.invalid"
	phone := "1" + strings.Map(func(r rune) rune { return '0' + r%10 }, accountPassword(t))[:10]
	normalized, err := identity.NormalizePhone(phone)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=?,phone=? WHERE id=?", email, normalized, f.user.ID))
	accounts := []string{f.user.Username, phone, normalized, strings.ToUpper(email)}
	for _, account := range accounts {
		w := f.request(t, "POST", "/auth/login", "", map[string]any{"account": account, "password": f.password})
		require.Equal(t, 200, w.Code)
	}
	for i := 0; i < 5; i++ {
		_, err := f.service.Login(f.ctx, 1, accounts[i%len(accounts)], accountPassword(t), identity.RequestMeta{IP: fmt.Sprintf("192.0.2.%d", i+20)})
		if i < 4 {
			require.Equal(t, 20101, errs.From(err).Code)
		} else {
			require.Equal(t, 20104, errs.From(err).Code)
		}
	}
	for i, account := range accounts {
		_, err := f.service.Login(f.ctx, 1, account, f.password, identity.RequestMeta{IP: fmt.Sprintf("192.0.2.%d", i+40)})
		require.Equal(t, 20104, errs.From(err).Code)
	}
}
