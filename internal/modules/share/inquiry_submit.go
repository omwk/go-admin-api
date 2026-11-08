package share

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// SubmitInquiry admits a self-reported lead without returning its persistent identity.
func (s *LinkService) SubmitInquiry(ctx context.Context, tenant int64, token, ip, cookie, key string, in InquiryInput) error {
	row, content, err := s.accessContent(ctx, tenant, token, ip)
	if err != nil {
		return err
	}
	credential, valid := s.readCredential(cookie, row, content.ContentVersion)
	if !valid || !credential.Admitted || !row.AllowInquiry || s.now().UTC().Unix()-credential.IssuedAt < 2 {
		return AccessDenied()
	}
	in, err = normalizeInquiry(in, content)
	if err != nil {
		return err
	}
	store, ok := s.store.(InquiryStore)
	if !ok {
		return unavailable()
	}
	receipt, err := s.inquiryReceipt(row, credential, key, in)
	if err != nil {
		return err
	}
	_, replay, err := store.Receipt(ctx, receipt, s.now().UTC())
	if err != nil {
		return unavailable()
	}
	if !replay {
		if err := s.budget(ctx, fmt.Sprintf("share:inquiry:ip:v1:%d:%s", tenant, tokenHash(ip)), 1.0/20, 3); err != nil {
			return err
		}
		if err := s.budget(ctx, fmt.Sprintf("share:inquiry:link:v1:%d:%d", tenant, row.ID), 1, 30); err != nil {
			return err
		}
		if err := s.budget(ctx, fmt.Sprintf("share:inquiry:all:v1:%d", tenant), 10, 100); err != nil {
			return err
		}
	}
	row.FrozenVersion = content.ContentVersion
	return s.persistInquiry(ctx, store, row, receipt, in)
}

func (s *LinkService) persistInquiry(ctx context.Context, store InquiryStore, link Link, receipt InquiryReceipt, in InquiryInput) error {
	err := store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.Admit(tx, link, false, s.now().UTC()); err != nil {
			return accessError(err)
		}
		previous, exists, err := store.Receipt(tx, receipt, s.now().UTC())
		if err != nil {
			return unavailable()
		}
		if exists {
			if previous.RequestHash != receipt.RequestHash {
				return errs.New(errs.ErrIdempotencyMismatch, 409, "链接暂不可用")
			}
			return nil
		}
		id, err := store.NextID(tx, "inquiry")
		if err != nil {
			return unavailable()
		}
		row := Inquiry{ID: id, TenantID: link.TenantID, ShareLinkID: link.ID, CustomerID: link.CustomerID, OwnerUserID: link.CreatedBy,
			Revision: 1, Status: "new", CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}
		if err := sealInquiry(s.sealer, &row, in); err != nil {
			return err
		}
		receipt.InquiryID = id
		if err := store.InsertInquiry(tx, &row, &receipt); err != nil {
			return unavailable()
		}
		return nil
	})
	return err
}

func (s *LinkService) inquiryReceipt(row Link, browser browserCredential, key string, in InquiryInput) (InquiryReceipt, error) {
	indexer, ok := s.sealer.(crypto.BlindIndexer)
	if !ok || len(key) > 128 || strings.ContainsAny(key, "\r\n\x00") {
		return InquiryReceipt{}, AccessDenied()
	}
	if key == "" {
		var err error
		key, err = generateToken(rand.Reader)
		if err != nil {
			return InquiryReceipt{}, unavailable()
		}
	}
	domain := fmt.Sprintf("share:inquiry:v1:%d:%d:%d:%s", row.TenantID, row.ID, row.ContentVersion, browser.Nonce)
	scope := tokenHash(domain)
	keyHash := tokenHash(key)
	raw, err := json.Marshal(in)
	if err != nil {
		return InquiryReceipt{}, unavailable()
	}
	defer clear(raw)
	digest, err := indexer.BlindIndex(domain+":"+keyHash, raw)
	return InquiryReceipt{TenantID: row.TenantID, ShareLinkID: row.ID, ScopeHash: scope, KeyHash: keyHash, RequestHash: digest,
		ExpiresAt: s.now().UTC().Add(24 * time.Hour)}, err
}

func normalizeInquiry(in InquiryInput, content PublicContent) (InquiryInput, error) {
	in.ContactName, in.ContactPhone = strings.TrimSpace(in.ContactName), strings.TrimSpace(in.ContactPhone)
	in.ContactEmail, in.Company, in.Message = strings.TrimSpace(in.ContactEmail), strings.TrimSpace(in.Company), strings.TrimSpace(in.Message)
	if in.Website != "" || in.ContactName == "" || in.ContactPhone == "" && in.ContactEmail == "" || len(in.Items) > 100 {
		return InquiryInput{}, AccessDenied()
	}
	for _, f := range []struct {
		value string
		limit int
	}{{in.ContactName, 100}, {in.ContactPhone, 100}, {in.ContactEmail, 254}, {in.Company, 200}, {in.Message, 2000}} {
		if !utf8.ValidString(f.value) || utf8.RuneCountInString(f.value) > f.limit || strings.ContainsRune(f.value, 0) {
			return InquiryInput{}, AccessDenied()
		}
	}
	seen := map[[2]int64]bool{}
	for i := range in.Items {
		item := &in.Items[i]
		item.Note = strings.TrimSpace(item.Note)
		if item.Qty <= 0 || item.Qty > 1000000 || utf8.RuneCountInString(item.Note) > 2000 || !utf8.ValidString(item.Note) {
			return InquiryInput{}, AccessDenied()
		}
		found := false
		for _, member := range content.Items {
			found = found || member.SPUID == item.SPUID && (item.SKUID == 0 || member.SKUID == item.SKUID)
		}
		identity := [2]int64{item.SPUID, item.SKUID}
		if !found || seen[identity] {
			return InquiryInput{}, AccessDenied()
		}
		seen[identity] = true
	}
	sort.Slice(in.Items, func(i, j int) bool {
		if in.Items[i].SPUID == in.Items[j].SPUID {
			return in.Items[i].SKUID < in.Items[j].SKUID
		}
		return in.Items[i].SPUID < in.Items[j].SPUID
	})
	return in, nil
}
