// Package model defines tenant file metadata.
package model

import "time"

// FileObject records metadata for an object stored outside PostgreSQL.
type FileObject struct {
	ProviderID     *int64    `gorm:"column:provider_id"`
	ID             int64     `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID       int64     `gorm:"column:tenant_id;not null;default:1"`
	Bucket         string    `gorm:"column:bucket;not null"`
	ObjectKey      string    `gorm:"column:object_key;not null"`
	OriginalName   string    `gorm:"column:original_name;not null"`
	MIME           string    `gorm:"column:mime;not null"`
	SizeBytes      int64     `gorm:"column:size_bytes;not null"`
	SHA256         string    `gorm:"column:sha256;not null"`
	Purpose        string    `gorm:"column:purpose;not null"`
	Category       string    `gorm:"column:category"`
	ThumbKey       string    `gorm:"column:thumb_key"`
	UploadedBy     *int64    `gorm:"column:uploaded_by"`
	UploadedByName string    `gorm:"column:uploaded_by_name;->;-:migration" json:"-"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

// TableName returns the file object table.
func (FileObject) TableName() string { return "file_object" }
