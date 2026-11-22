package billing

import "time"

type CheckoutRecord struct {
	PromptID       string    `gorm:"primaryKey;type:text"`
	CandidateID    string    `gorm:"type:text;not null;index:idx_candidate_checkouts_candidate_created,priority:1"`
	PlanID         string    `gorm:"type:text;not null"`
	Route          string    `gorm:"type:text;not null;default:''"`
	Status         string    `gorm:"type:text;not null;default:pending;index:idx_candidate_checkouts_status,priority:1,where:status = 'pending'"`
	SubscriptionID string    `gorm:"type:text;not null;default:''"`
	AmountCents    int64     `gorm:"not null;default:0"`
	Currency       string    `gorm:"type:text;not null;default:''"`
	Country        string    `gorm:"type:text;not null;default:''"`
	RedirectURL    string    `gorm:"type:text;not null;default:''"`
	Error          string    `gorm:"type:text;not null;default:''"`
	CreatedAt      time.Time `gorm:"not null;default:now();index:idx_candidate_checkouts_candidate_created,priority:2,sort:desc;index:idx_candidate_checkouts_status,priority:2,where:status = 'pending'"`
	UpdatedAt      time.Time `gorm:"not null;default:now()"`
	// ActivatedAt marks a paid checkout whose entitlement was granted.
	ActivatedAt *time.Time `gorm:"type:timestamptz;index:idx_candidate_checkouts_paid_unactivated,where:status = 'paid' AND activated_at IS NULL"`
	// ActivationClaimedAt is the lease taken before granting the entitlement,
	// so concurrent webhook / status-poll / reconciler calls cannot both run
	// the side effects (subscription flip, one-time report email).
	ActivationClaimedAt *time.Time `gorm:"type:timestamptz"`
	// ActivationAttempts counts claims; retries stop at MaxActivationAttempts.
	ActivationAttempts int `gorm:"not null;default:0"`
	// NextActivationAt is the backoff gate for the next reconciler retry.
	NextActivationAt *time.Time `gorm:"type:timestamptz"`
	// ActivationError is the last activation failure, for operators.
	ActivationError string `gorm:"type:text;not null;default:''"`
	// PaidAt is when the checkout moved to paid (first time); "newer
	// purchase" decisions compare payment time, not checkout creation.
	PaidAt *time.Time `gorm:"type:timestamptz"`
	// ActivationOutcome: applied | superseded (a newer purchase was already
	// active; review for refund) | already_held.
	ActivationOutcome string `gorm:"type:text;not null;default:''"`
}

func (CheckoutRecord) TableName() string { return "candidate_checkouts" }

func Schema() []any { return []any{&CheckoutRecord{}} }
