package share

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
)

func inquiryFields(row *Inquiry) []*Envelope {
	return []*Envelope{&row.ContactName, &row.ContactPhone, &row.ContactEmail, &row.Company, &row.Message, &row.Items}
}

var inquiryFieldNames = []string{"contact_name", "contact_phone", "contact_email", "company", "message", "items"}

// InquiryAAD prevents field substitution across records, tenants and revisions.
func InquiryAAD(tenant, id int64, field string, revision int) (string, error) {
	for _, name := range inquiryFieldNames {
		if field == name && tenant > 0 && id > 0 && revision > 0 {
			return fmt.Sprintf("%d:inquiry:%d:%s:%d", tenant, id, field, revision), nil
		}
	}
	return "", errors.New("invalid inquiry field identity")
}

func sealInquiry(sealer crypto.Sealer, row *Inquiry, in InquiryInput) error {
	if sealer == nil {
		return unavailable()
	}
	items, err := json.Marshal(in.Items)
	if err != nil {
		return unavailable()
	}
	defer clear(items)
	values := [][]byte{[]byte(in.ContactName), []byte(in.ContactPhone), []byte(in.ContactEmail), []byte(in.Company), []byte(in.Message), items}
	for i, target := range inquiryFields(row) {
		aad, err := InquiryAAD(row.TenantID, row.ID, inquiryFieldNames[i], row.Revision)
		if err != nil {
			return err
		}
		cipher, nonce, key, err := sealer.Seal(values[i], aad)
		clear(values[i])
		if err != nil || len(cipher) < 16 || len(nonce) != 12 || key <= 0 || row.KeyVersion != 0 && row.KeyVersion != key {
			return unavailable()
		}
		*target, row.KeyVersion = Envelope{Cipher: cipher, Nonce: nonce}, key
	}
	return nil
}

func openInquiry(sealer crypto.Sealer, row Inquiry) (InquiryInput, error) {
	if sealer == nil || row.PIIPurgedAt != nil {
		return InquiryInput{}, unavailable()
	}
	plain := make([][]byte, len(inquiryFieldNames))
	defer func() {
		for _, value := range plain {
			clear(value)
		}
	}()
	for i, field := range inquiryFields(&row) {
		aad, err := InquiryAAD(row.TenantID, row.ID, inquiryFieldNames[i], row.Revision)
		if err != nil {
			return InquiryInput{}, unavailable()
		}
		plain[i], err = sealer.Open(field.Cipher, field.Nonce, aad, row.KeyVersion)
		if err != nil {
			return InquiryInput{}, unavailable()
		}
	}
	var items []InquiryItem
	if json.Unmarshal(plain[5], &items) != nil {
		return InquiryInput{}, unavailable()
	}
	return InquiryInput{ContactName: string(plain[0]), ContactPhone: string(plain[1]), ContactEmail: string(plain[2]), Company: string(plain[3]), Message: string(plain[4]), Items: items}, nil
}
