package gateway

import (
	"context"
	"time"
)

// ClientInfo is the management projection without key material.
type ClientInfo struct {
	ID          int64    `json:"id"`
	AppID       string   `json:"app_id"`
	Name        string   `json:"name"`
	OwnerUserID int64    `json:"owner_user_id"`
	Status      string   `json:"status"`
	Version     int      `json:"version"`
	IPWhitelist []string `json:"ip_whitelist"`
}

// RouteGrantInfo is the management projection for one client route grant.
type RouteGrantInfo struct {
	ID           int64      `json:"id"`
	RouteID      int64      `json:"route_id"`
	RouteCode    string     `json:"route_code"`
	RouteName    string     `json:"route_name"`
	Method       string     `json:"method"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`
	QuotaDaily   int64      `json:"quota_daily"`
	QuotaMonthly int64      `json:"quota_monthly"`
	GrantedAt    time.Time  `json:"granted_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Version      int        `json:"version"`
}

// SecretInfo deliberately excludes ciphertext, fingerprint and plaintext.
type SecretInfo struct {
	ID           int64      `json:"id"`
	SecretPrefix string     `json:"secret_prefix"`
	Status       string     `json:"status"`
	Revealable   bool       `json:"revealable"`
	ExpiresAt    *time.Time `json:"expires_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

// RevealedSecret is the response of the audited secret reveal operation.
type RevealedSecret struct {
	Secret    string     `json:"secret"`
	Prefix    string     `json:"prefix"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// ManagementStore applies tenant/owner predicates before management operations.
type ManagementStore interface {
	ListClients(context.Context, Actor) ([]ClientInfo, error)
	GetClient(context.Context, Actor, int64) (ClientInfo, error)
	ListRoutes(context.Context, Actor) ([]Route, error)
	ListGrants(context.Context, Actor, int64) ([]RouteGrantInfo, error)
	SaveRoute(context.Context, Actor, Route) (Route, error)
	GrantRoute(context.Context, Actor, int64, int64, string, *time.Time, QuotaLimits) error
	RevokeGrant(context.Context, Actor, int64, int64) error
}

// ClientView constructs the management DTO explicitly.
func ClientView(c Client) ClientInfo {
	return ClientInfo{ID: c.ID, AppID: c.AppID, Name: c.Name, OwnerUserID: c.OwnerUserID, Status: c.Status, Version: c.Version, IPWhitelist: append([]string{}, c.IPWhitelist...)}
}
