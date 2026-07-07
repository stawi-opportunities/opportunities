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
}

func (CheckoutRecord) TableName() string { return "candidate_checkouts" }

func Schema() []any { return []any{&CheckoutRecord{}} }
