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

// SecretInfo deliberately excludes ciphertext, fingerprint and plaintext.
type SecretInfo struct {
	ID           int64      `json:"id"`
	SecretPrefix string     `json:"secret_prefix"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

// ManagementStore applies tenant/owner predicates before management operations.
type ManagementStore interface {
	ListClients(context.Context, Actor) ([]ClientInfo, error)
	ListSecrets(context.Context, Actor, int64) ([]SecretInfo, error)
	ListRoutes(context.Context, Actor) ([]Route, error)
	SaveRoute(context.Context, Actor, Route) (Route, error)
	GrantRoute(context.Context, Actor, int64, int64, string, *time.Time) error
}

// ClientView constructs the management DTO explicitly.
func ClientView(c Client) ClientInfo {
	return ClientInfo{ID: c.ID, AppID: c.AppID, Name: c.Name, OwnerUserID: c.OwnerUserID, Status: c.Status, Version: c.Version, IPWhitelist: append([]string{}, c.IPWhitelist...)}
}
