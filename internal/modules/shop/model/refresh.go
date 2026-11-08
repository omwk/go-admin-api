package model

import "time"

// RefreshRun retains one actual dispatch attempt and its durable recovery phase.
type RefreshRun struct {
	ID               int64 `gorm:"primaryKey"`
	TenantID         int64
	CredentialID     int64
	InputVersionID   int64
	Generation       int64
	CandidateVersion int
	StagedResultID   *int64
	OutputVersionID  *int64
	Phase            string
	Status           string
	FailureClass     string
	Reason           string
	Attempt          int
	Trigger          string
	SendDeadline     *time.Time
	NextRetryAt      *time.Time
	StartedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       *time.Time
	TraceID          string
}

// TableName returns the persistent refresh attempt table.
func (RefreshRun) TableName() string { return "credential_refresh_run" }

// RefreshStage holds only encrypted candidate values, bound to their reserved AAD.
type RefreshStage struct {
	ID           int64      `gorm:"primaryKey" json:"-"`
	TenantID     int64      `json:"-"`
	CredentialID int64      `json:"-"`
	RunID        int64      `json:"-"`
	VersionNo    int        `json:"-"`
	CipherText   []byte     `json:"-"`
	Nonce        []byte     `json:"-"`
	KeyVersion   int        `json:"-"`
	AAD          string     `json:"-"`
	Fingerprint  string     `json:"-"`
	ExpiresAt    time.Time  `json:"-"`
	RefreshAt    time.Time  `json:"-"`
	CreatedAt    time.Time  `json:"-"`
	PurgeAfter   time.Time  `json:"-"`
	PurgedAt     *time.Time `json:"-"`
}

// TableName returns the short-lived encrypted result table.
func (RefreshStage) TableName() string { return "credential_refresh_stage" }

// RefreshReconciliation records ambiguous consumption without scheduling another send.
type RefreshReconciliation struct {
	ID           int64 `gorm:"primaryKey"`
	TenantID     int64
	CredentialID int64
	RunID        int64
	Reason       string
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}

// TableName returns the reconciliation record table.
func (RefreshReconciliation) TableName() string { return "credential_refresh_reconciliation" }
