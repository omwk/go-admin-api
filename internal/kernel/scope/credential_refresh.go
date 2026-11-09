package scope

import (
	"context"
	"errors"
)

// CredentialRefreshAccess authorizes only maintenance of one durable credential run.
// Private fields and rejected serialization keep queue and HTTP input from minting it.
type CredentialRefreshAccess struct {
	tenant, shop, credential, version, run, generation int64
	purpose                                            string
}

// IssueCredentialRefreshAccess accepts only bootstrap-verified durable relationships.
func IssueCredentialRefreshAccess(_ BootstrapIssuer, tenant, shop, credential, version, run, generation int64, purpose string) (CredentialRefreshAccess, error) {
	if tenant <= 0 || shop <= 0 || credential <= 0 || version <= 0 || run <= 0 || generation <= 0 || purpose != "credential.refresh" {
		return CredentialRefreshAccess{}, Denied()
	}
	return CredentialRefreshAccess{tenant, shop, credential, version, run, generation, purpose}, nil
}

func (a CredentialRefreshAccess) TenantID() int64       { return a.tenant }
func (a CredentialRefreshAccess) ShopID() int64         { return a.shop }
func (a CredentialRefreshAccess) CredentialID() int64   { return a.credential }
func (a CredentialRefreshAccess) InputVersionID() int64 { return a.version }
func (a CredentialRefreshAccess) RunID() int64          { return a.run }
func (a CredentialRefreshAccess) Generation() int64     { return a.generation }
func (a CredentialRefreshAccess) Purpose() string       { return a.purpose }

// MarshalJSON prevents exporting a capability to an untrusted transport.
func (CredentialRefreshAccess) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential refresh access cannot be serialized")
}

// UnmarshalJSON rejects forged capabilities, including job/resource-shaped input.
func (*CredentialRefreshAccess) UnmarshalJSON([]byte) error {
	return errors.New("credential refresh access cannot be deserialized")
}

type credentialRefreshKey struct{}

// WithCredentialRefreshAccess attaches the independent maintenance capability.
func WithCredentialRefreshAccess(ctx context.Context, a CredentialRefreshAccess) context.Context {
	return context.WithValue(ctx, credentialRefreshKey{}, a)
}

// CredentialRefreshFrom rejects absent, human, sync and zero-value identities.
func CredentialRefreshFrom(ctx context.Context) (CredentialRefreshAccess, error) {
	if ctx == nil {
		return CredentialRefreshAccess{}, Denied()
	}
	a, ok := ctx.Value(credentialRefreshKey{}).(CredentialRefreshAccess)
	if !ok {
		return CredentialRefreshAccess{}, Denied()
	}
	_, err := IssueCredentialRefreshAccess(BootstrapIssuer{}, a.tenant, a.shop, a.credential, a.version, a.run, a.generation, a.purpose)
	return a, err
}
