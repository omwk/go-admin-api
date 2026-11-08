package share

import (
	"errors"
	"fmt"
)

// FieldAAD binds ciphertext to its tenant, entity, record, field and revision.
func FieldAAD(tenant int64, entity string, id int64, field string, revision int) (string, error) {
	allowed := map[string]map[string]bool{"customer": {"contact_name": true, "phone": true, "email": true}, "price_list": {"note": true}, "price_list_item": {"note": true}}
	if tenant <= 0 || id <= 0 || revision <= 0 || !allowed[entity][field] {
		return "", errors.New("invalid pricing field identity")
	}
	return fmt.Sprintf("%d:%s:%d:%s:%d", tenant, entity, id, field, revision), nil
}

func (s *Service) seal(tenant int64, entity string, id int64, field string, revision int, value string) (Envelope, int, error) {
	if s.sealer == nil {
		return Envelope{}, 0, errors.New("pricing field encryption unavailable")
	}
	aad, err := FieldAAD(tenant, entity, id, field, revision)
	if err != nil {
		return Envelope{}, 0, err
	}
	plain := []byte(value)
	defer clear(plain)
	cipher, nonce, version, err := s.sealer.Seal(plain, aad)
	if err != nil || len(cipher) < 16 || len(nonce) != 12 || version <= 0 {
		return Envelope{}, 0, errors.New("seal pricing field failed")
	}
	return Envelope{Cipher: cipher, Nonce: nonce}, version, nil
}

func (s *Service) sealContact(row *Customer, in CustomerInput) error {
	fields := []struct {
		name, value string
		target      *Envelope
	}{{"contact_name", in.ContactName, &row.ContactName}, {"phone", in.Phone, &row.Phone}, {"email", in.Email, &row.Email}}
	for _, f := range fields {
		envelope, version, err := s.seal(row.TenantID, "customer", row.ID, f.name, row.Version, f.value)
		if err != nil {
			return err
		}
		if row.KeyVersion != 0 && row.KeyVersion != version {
			return errors.New("pricing key version changed")
		}
		*f.target = envelope
		row.KeyVersion = version
	}
	return nil
}

func customerView(row Customer) CustomerView {
	return CustomerView{Customer: row, ContactName: "*", Phone: "***********", Email: "***"}
}
