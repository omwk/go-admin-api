// Package system implements registered tenant settings and dictionary management.
package system

// Actor is the current authenticated tenant identity.
type Actor struct{ TenantID, UserID int64 }

// Page is the standard bounded management page.
type Page[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}
