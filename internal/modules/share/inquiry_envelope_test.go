package share

import (
	"crypto/rand"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func TestInquiryEnvelopeBindsTenantRecordFieldRevisionAndDomain(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	defer clear(key)
	row := Inquiry{TenantID: 1, ID: 21, Revision: 1}
	in := InquiryInput{ContactName: "synthetic-name", ContactPhone: "synthetic-contact", Company: "synthetic-company", Message: "synthetic-note"}
	require.NoError(t, sealInquiry(sealer, &row, in))
	opened, err := openInquiry(sealer, row)
	require.NoError(t, err)
	require.Equal(t, in.ContactName, opened.ContactName)
	for _, change := range []func(*Inquiry){
		func(r *Inquiry) { r.TenantID++ },
		func(r *Inquiry) { r.ID++ },
		func(r *Inquiry) { r.Revision++ },
		func(r *Inquiry) { r.ContactName, r.Company = r.Company, r.ContactName },
	} {
		changed := row
		change(&changed)
		out, err := openInquiry(sealer, changed)
		require.Error(t, err)
		require.Empty(t, out)
	}
	_, err = sealer.Open(row.ContactName.Cipher, row.ContactName.Nonce, "1:order:21:contact_name:1", 1)
	require.Error(t, err)
	_, err = InquiryAAD(1, 21, "unapproved", 1)
	require.Error(t, err)
}
