package bootstrap

import (
	"bytes"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderAddressIntegrationStoredFieldsAuthenticateIndependently(t *testing.T) {
	// REQ-ORD-007: authentication tests use stored bytea, without a PII read API.
	f := newOrderFixture(t)
	s := f.snapshot(t, "address-envelope")
	result, err := f.apply(s)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var address order.Address
	require.NoError(t, q.Select("tenant_id,order_id,receiver_name_cipher,receiver_name_nonce,phone_cipher,phone_nonce,address_cipher,address_nonce,key_version,revision").Where("tenant_id=1 AND order_id=?", result.OrderID).Take(&address).Error)
	require.False(t, bytes.Equal(address.ReceiverNameNonce, address.PhoneNonce))
	require.False(t, bytes.Equal(address.ReceiverNameNonce, address.AddressNonce))
	require.False(t, bytes.Equal(address.PhoneNonce, address.AddressNonce))
	for _, field := range []struct {
		name                 string
		plain, cipher, nonce []byte
	}{
		{"receiver_name", s.Address.ReceiverName, address.ReceiverNameCipher, address.ReceiverNameNonce},
		{"phone", s.Address.Phone, address.PhoneCipher, address.PhoneNonce},
		{"address", s.Address.Address, address.AddressCipher, address.AddressNonce},
	} {
		aad, err := order.AddressAAD(1, result.OrderID, field.name, address.Revision)
		require.NoError(t, err)
		plain, err := f.sealer.Open(field.cipher, field.nonce, aad, address.KeyVersion)
		require.NoError(t, err)
		require.True(t, bytes.Equal(plain, field.plain), "authenticated stored field differs")
		clear(plain)
		corrupt := bytes.Clone(field.cipher)
		corrupt[len(corrupt)-1] ^= 1
		_, err = f.sealer.Open(corrupt, field.nonce, aad, address.KeyVersion)
		require.Error(t, err)
	}
	next := f.snapshot(t, "address-envelope")
	next.ExpectedVersion = result.Version
	second, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	var revision int64
	require.NoError(t, q.Table("order_address").Select("revision").Where("tenant_id=1 AND order_id=?", result.OrderID).Take(&revision).Error)
	require.EqualValues(t, 2, revision)
	wrongAAD, err := order.AddressAAD(1, result.OrderID, "phone", revision)
	require.NoError(t, err)
	_, err = f.sealer.Open(address.PhoneCipher, address.PhoneNonce, wrongAAD, address.KeyVersion)
	require.Error(t, err)
}

type orderFailingSealer struct {
	interfaceSealer
	calls int
}

type interfaceSealer interface {
	crypto.Sealer
	crypto.BlindIndexer
}

func (s *orderFailingSealer) Seal(plain []byte, aad string) ([]byte, []byte, int, error) {
	s.calls++
	if s.calls == 3 {
		return nil, nil, 0, errors.New("injected third field seal failure")
	}
	return s.interfaceSealer.Seal(plain, aad)
}

func TestOrderAddressIntegrationThirdFieldFailureRollsBackWholeAggregate(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "encryption-failure")
	first, err := f.apply(s)
	require.NoError(t, err)
	require.Equal(t, "applied", first.Outcome)
	before := f.aggregate(t)
	sealer, ok := f.sealer.(interfaceSealer)
	require.True(t, ok)
	f.upserter, err = order.NewUpserter(&orderFailingSealer{interfaceSealer: sealer}, order.MoneyPolicy{FormulaToleranceCents: 2, PaymentToleranceCents: 2})
	require.NoError(t, err)
	next := f.snapshot(t, "encryption-failure")
	next.ExpectedVersion = first.Version
	_, err = f.apply(next)
	require.EqualError(t, err, "seal order address failed")
	require.Equal(t, before, f.aggregate(t))
}
