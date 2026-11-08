// Package share owns customer pricing and the administrative quote boundary.
package share

import "time"

// Envelope holds one authenticated field; its bytes never enter public DTOs.
type Envelope struct {
	Cipher []byte `json:"-"`
	Nonce  []byte `json:"-"`
}

// Customer is a tenant-local customer with independently sealed contact fields.
type Customer struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"-"`
	Code         string     `json:"code"`
	Name         string     `json:"name"`
	ContactName  Envelope   `gorm:"embedded;embeddedPrefix:contact_name_" json:"-"`
	Phone        Envelope   `gorm:"embedded;embeddedPrefix:phone_" json:"-"`
	Email        Envelope   `gorm:"embedded;embeddedPrefix:email_" json:"-"`
	KeyVersion   int        `json:"-"`
	Version      int        `json:"version"`
	Status       string     `json:"status"`
	OwnerUserID  int64      `json:"owner_user_id"`
	Source       string     `json:"source"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"-"`
	TerminatedAt *time.Time `json:"-"`
}

// TableName returns the fixed business table.
func (Customer) TableName() string { return "customer" }

// CustomerView always exposes masked contact values, including on write responses.
type CustomerView struct {
	Customer
	ContactName string `json:"contact_name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
}

// PriceList is an explicit customer or tenant-default price source.
type PriceList struct {
	ID         int64      `json:"id"`
	TenantID   int64      `json:"-"`
	Type       string     `json:"type"`
	CustomerID *int64     `json:"customer_id"`
	Name       string     `json:"name"`
	Currency   string     `json:"currency"`
	Status     string     `json:"status"`
	ValidFrom  *time.Time `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to"`
	Note       Envelope   `gorm:"embedded;embeddedPrefix:note_" json:"-"`
	KeyVersion int        `json:"-"`
	Revision   int        `json:"-"`
	Version    int        `json:"version"`
	CreatedBy  int64      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"-"`
}

// TableName returns the fixed price-list table.
func (PriceList) TableName() string { return "price_list" }

// Item binds an exact immutable product identity and sealed internal note.
type Item struct {
	ID          int64     `json:"id"`
	TenantID    int64     `json:"-"`
	PriceListID int64     `json:"price_list_id"`
	SPUID       int64     `json:"spu_id" gorm:"column:spu_id"`
	SKUID       int64     `json:"sku_id" gorm:"column:sku_id"`
	PriceCents  int64     `json:"price_cents"`
	MOQ         int       `json:"moq"`
	Note        Envelope  `gorm:"embedded;embeddedPrefix:note_" json:"-"`
	KeyVersion  int       `json:"-"`
	Revision    int       `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName returns the fixed item table.
func (Item) TableName() string { return "price_list_item" }

// ListDetail includes a price source and its safe item projections.
type ListDetail struct {
	PriceList
	Items []Item `json:"items"`
}
