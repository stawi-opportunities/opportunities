package savedjobs

import "time"

type Record struct {
	CandidateID   string    `gorm:"primaryKey;type:text;index:idx_candidate_saved_jobs_candidate_created,priority:1"`
	OpportunityID string    `gorm:"primaryKey;type:text"`
	CreatedAt     time.Time `gorm:"not null;default:now();index:idx_candidate_saved_jobs_candidate_created,priority:2,sort:desc"`
}

func (Record) TableName() string { return "candidate_saved_jobs" }

func Schema() []any { return []any{&Record{}} }
