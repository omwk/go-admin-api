package share

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"golang.org/x/crypto/bcrypt"
)

// AccessDenied hides absent, expired, revoked and unauthorized resources alike.
func AccessDenied() error { return errs.New(70102, 404, "链接暂不可用") }

func passwordRequired() error { return errs.New(70103, 401, "链接暂不可用") }
func rateLimited() error      { return errs.New(errs.ErrTooManyRequests, 429, "链接暂不可用") }

// Browse discloses only frozen members after a fresh primary-database admission.
func (s *LinkService) Browse(ctx context.Context, tenant int64, token, ip, cookie string, spu int64) (PublicContent, string, time.Time, error) {
	row, content, err := s.accessContent(ctx, tenant, token, ip)
	if err != nil {
		return PublicContent{}, "", time.Time{}, err
	}
	credential, valid := s.readCredential(cookie, row, content.ContentVersion)
	if spu > 0 && (!valid || !credential.Admitted) {
		return PublicContent{}, "", time.Time{}, passwordRequired()
	}
	if row.PasswordHash != nil && !valid {
		return PublicContent{}, "", time.Time{}, passwordRequired()
	}
	if spu > 0 {
		items := []PublicItem{}
		for _, item := range content.Items {
			if item.SPUID == spu {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			return PublicContent{}, "", time.Time{}, AccessDenied()
		}
		content.Items = items
	}
	if !valid {
		nonce, err := generateToken(rand.Reader)
		if err != nil {
			return PublicContent{}, "", time.Time{}, unavailable()
		}
		expires := earlier(timePointer(s.now().UTC().Add(2*time.Hour)), row.ExpiresAt)
		credential = browserCredential{IssuedAt: s.now().UTC().Unix(), ExpiresAt: expires.Unix(), Nonce: nonce}
	}
	credential.Admitted = true
	sealed, expires, err := s.sealCredential(row, content.ContentVersion, credential)
	if err != nil {
		return PublicContent{}, "", time.Time{}, unavailable()
	}
	row.FrozenVersion = content.ContentVersion
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.Admit(tx, row, spu == 0, s.now().UTC()); err != nil {
			return err
		}
		result := "admitted"
		if spu > 0 {
			result = "detail"
		}
		return s.store.RecordVisit(tx, row, result, s.now().UTC())
	})
	if err != nil {
		return PublicContent{}, "", time.Time{}, accessError(err)
	}
	return content, sealed, expires, nil
}

// Verify validates the password without spending a root view or admitting details.
func (s *LinkService) Verify(ctx context.Context, tenant int64, token, ip, password string) (string, time.Time, error) {
	if len(password) > 72 {
		return "", time.Time{}, passwordRequired()
	}
	row, content, err := s.accessContent(ctx, tenant, token, ip)
	if err != nil {
		return "", time.Time{}, err
	}
	key := fmt.Sprintf("share:verify:v1:%d:%d:%s", tenant, row.ID, tokenHash(ip))
	if err := s.budget(ctx, fmt.Sprintf("share:verify:ip:v1:%d:%s", tenant, tokenHash(ip)), 1.0/6, 10); err != nil {
		return "", time.Time{}, err
	}
	_, blocked, err := s.cache.Get(ctx, key+":cooldown")
	if err != nil {
		return "", time.Time{}, unavailable()
	}
	if blocked {
		return "", time.Time{}, rateLimited()
	}
	select {
	case s.verifySlots <- struct{}{}:
		defer func() { <-s.verifySlots }()
	default:
		return "", time.Time{}, rateLimited()
	}
	if row.PasswordHash != nil && bcrypt.CompareHashAndPassword([]byte(*row.PasswordHash), []byte(password)) != nil {
		allowed, remaining, _, err := s.cache.TokenBucket(ctx, key+":fail", 1.0/180, 5)
		if err != nil {
			return "", time.Time{}, unavailable()
		}
		if !allowed || remaining == 0 {
			if err := s.cache.Set(ctx, key+":cooldown", "1", 15*time.Minute); err != nil {
				return "", time.Time{}, unavailable()
			}
			return "", time.Time{}, rateLimited()
		}
		return "", time.Time{}, passwordRequired()
	}
	nonce, err := generateToken(rand.Reader)
	if err != nil {
		return "", time.Time{}, unavailable()
	}
	expires := earlier(timePointer(s.now().UTC().Add(2*time.Hour)), row.ExpiresAt)
	sealed, expiresAt, err := s.sealCredential(row, content.ContentVersion, browserCredential{IssuedAt: s.now().UTC().Unix(), ExpiresAt: expires.Unix(), Nonce: nonce})
	if err != nil {
		return "", time.Time{}, unavailable()
	}
	row.FrozenVersion = content.ContentVersion
	err = s.store.Transaction(ctx, func(tx context.Context) error { return s.store.Admit(tx, row, false, s.now().UTC()) })
	if err != nil {
		return "", time.Time{}, accessError(err)
	}
	return sealed, expiresAt, nil
}

func (s *LinkService) accessContent(ctx context.Context, tenant int64, token, ip string) (Link, PublicContent, error) {
	if s.cache == nil || s.sealer == nil || tenant <= 0 {
		return Link{}, PublicContent{}, unavailable()
	}
	if err := s.budget(ctx, fmt.Sprintf("share:entry:v1:%d:%s", tenant, tokenHash(ip)), 10, 60); err != nil {
		return Link{}, PublicContent{}, err
	}
	if err := s.budget(ctx, fmt.Sprintf("share:entry:all:v1:%d", tenant), 1000, 2000); err != nil {
		return Link{}, PublicContent{}, err
	}
	if !validToken(token) {
		return Link{}, PublicContent{}, AccessDenied()
	}
	row, err := s.store.ResolveLink(ctx, tenant, tokenHash(token))
	if err != nil {
		return Link{}, PublicContent{}, accessError(err)
	}
	if row.Status != "active" || row.ExpiresAt != nil && !s.now().UTC().Before(*row.ExpiresAt) {
		return Link{}, PublicContent{}, AccessDenied()
	}
	if err := s.budget(ctx, fmt.Sprintf("share:read:v1:%d:%d:%s", tenant, row.ID, tokenHash(ip)), 1, 60); err != nil {
		return Link{}, PublicContent{}, err
	}
	if err := s.budget(ctx, fmt.Sprintf("share:read:all:v1:%d:%d", tenant, row.ID), 10, 600); err != nil {
		return Link{}, PublicContent{}, err
	}
	content, err := s.cachedContent(ctx, row)
	return row, content, err
}

func (s *LinkService) cachedContent(ctx context.Context, row Link) (PublicContent, error) {
	key := fmt.Sprintf("share:content:v1:%d:%d:%d", row.TenantID, row.ID, row.ContentVersion)
	value, found, err := s.cache.Get(ctx, key)
	if err != nil {
		return PublicContent{}, unavailable()
	}
	if found {
		var out PublicContent
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&out) != nil || out.ContentVersion == "" {
			return PublicContent{}, unavailable()
		}
		return out, nil
	}
	frozen, err := s.store.Frozen(ctx, row.TenantID, row.ID)
	if err != nil {
		return PublicContent{}, accessError(err)
	}
	value, err = publicJSON(frozen.Content)
	if err != nil {
		return PublicContent{}, unavailable()
	}
	if err := s.cache.Set(ctx, key, value, 5*time.Minute); err != nil {
		return PublicContent{}, unavailable()
	}
	return frozen.Content, nil
}

func (s *LinkService) budget(ctx context.Context, key string, rate float64, burst int) error {
	allowed, _, _, err := s.cache.TokenBucket(ctx, key, rate, burst)
	if err != nil {
		return unavailable()
	}
	if !allowed {
		return rateLimited()
	}
	return nil
}

func accessError(err error) error {
	var app *errs.AppError
	if errors.As(err, &app) && (app.Code == errs.ErrNotFound || app.Code == 70102) {
		return AccessDenied()
	}
	return unavailable()
}

func timePointer(value time.Time) *time.Time { return &value }
