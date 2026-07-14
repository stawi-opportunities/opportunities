package matching

import (
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// CandidateMatchIndexRecord is mutable candidate matching configuration.
// The pgvector embedding column and HNSW index are added by capability SQL;
// GORM owns every ordinary column.
type CandidateMatchIndexRecord struct {
	CandidateID    string         `gorm:"primaryKey;type:text"`
	MinScore       float64        `gorm:"not null;default:0.5"`
	DailyCap       int            `gorm:"not null;default:25"`
	WeeklyCap      int            `gorm:"not null;default:100"`
	Kinds          pq.StringArray `gorm:"type:text[];not null;default:'{job}'"`
	Countries      pq.StringArray `gorm:"type:text[];not null;default:'{}'"`
	SalaryFloorUSD *int
	RemoteOnly     bool      `gorm:"not null;default:false"`
	Enabled        bool      `gorm:"not null;default:true"`
	UpdatedAt      time.Time `gorm:"not null;default:now()"`
}

func (CandidateMatchIndexRecord) TableName() string { return "candidate_match_indexes" }

type CandidatePreferenceRecord struct {
	CandidateID string          `gorm:"primaryKey;type:text"`
	OptIns      json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	UpdatedAt   time.Time       `gorm:"not null;default:now()"`
}

func (CandidatePreferenceRecord) TableName() string { return "candidate_preferences" }

type MatchRuleRecord struct {
	CandidateID string          `gorm:"primaryKey;type:text"`
	Document    json.RawMessage `gorm:"type:jsonb;not null"`
	Version     int             `gorm:"not null;default:1"`
	Enabled     bool            `gorm:"not null;default:true"`
	Autoapply   bool            `gorm:"not null;default:false"`
	UpdatedAt   time.Time       `gorm:"not null;default:now()"`
}

func (MatchRuleRecord) TableName() string { return "match_rules" }

type CandidateMatchRecord struct {
	MatchID       string  `gorm:"primaryKey;type:text"`
	CandidateID   string  `gorm:"type:text;not null;uniqueIndex:candidate_matches_pair_uniq,priority:1;index:candidate_matches_candidate_status_score_idx,priority:1;index:candidate_matches_candidate_created_idx,priority:1"`
	OpportunityID string  `gorm:"type:text;not null;uniqueIndex:candidate_matches_pair_uniq,priority:2;index:candidate_matches_opportunity_idx"`
	Status        string  `gorm:"type:text;not null;default:new;index:candidate_matches_candidate_status_score_idx,priority:2"`
	Score         float64 `gorm:"not null;index:candidate_matches_candidate_status_score_idx,priority:3,sort:desc"`
	RerankScore   *float64
	RerankerUsed  bool `gorm:"not null;default:false"`
	ViewedAt      *time.Time
	AppliedAt     *time.Time
	DismissedAt   *time.Time
	LastEventID   *string         `gorm:"type:text"`
	Metadata      json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt     time.Time       `gorm:"not null;default:now();index:candidate_matches_candidate_status_score_idx,priority:4,sort:desc;index:candidate_matches_candidate_created_idx,priority:2,sort:desc"`
	UpdatedAt     time.Time       `gorm:"not null;default:now()"`
}

func (CandidateMatchRecord) TableName() string { return "candidate_matches" }

type CandidateMatchEventRecord struct {
	EventID       string    `gorm:"primaryKey;type:text"`
	OccurredAt    time.Time `gorm:"primaryKey;not null;default:now();index:candidate_match_events_candidate_time_idx,priority:2,sort:desc"`
	CandidateID   string    `gorm:"type:text;not null;index:candidate_match_events_candidate_time_idx,priority:1"`
	OpportunityID string    `gorm:"type:text;not null"`
	CanonicalID   string    `gorm:"type:text;not null"`
	Kind          string    `gorm:"type:text;not null"`
	Path          string    `gorm:"type:text;not null"`
	Score         *float64
	RerankScore   *float64
	RerankerUsed  bool            `gorm:"not null;default:false"`
	Data          json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
}

func (CandidateMatchEventRecord) TableName() string { return "candidate_match_events" }

type MatchRunEventRecord struct {
	RunID             string    `gorm:"primaryKey;type:text"`
	StartedAt         time.Time `gorm:"primaryKey;not null;default:now()"`
	FinishedAt        *time.Time
	Path              string  `gorm:"type:text;not null"`
	TriggeredBy       string  `gorm:"type:text;not null"`
	CandidateID       *string `gorm:"type:text"`
	CanonicalID       *string `gorm:"type:text"`
	CandidatesScanned int     `gorm:"not null;default:0"`
	MatchesWritten    int     `gorm:"not null;default:0"`
	Status            string  `gorm:"type:text;not null"`
	RerankerStatus    *string `gorm:"type:text"`
	LatencyMS         *int
	Data              json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
}

func (MatchRunEventRecord) TableName() string { return "match_run_events" }

type EngagementEventRecord struct {
	EventID       string          `gorm:"primaryKey;type:text"`
	OccurredAt    time.Time       `gorm:"primaryKey;not null;default:now();index:engagement_events_opp_time_idx,priority:2,sort:desc;index:engagement_events_candidate_time_idx,priority:2,sort:desc,where:candidate_id IS NOT NULL"`
	CandidateID   *string         `gorm:"type:text;index:engagement_events_candidate_time_idx,priority:1,where:candidate_id IS NOT NULL"`
	OpportunityID string          `gorm:"type:text;not null;index:engagement_events_opp_time_idx,priority:1"`
	Kind          string          `gorm:"type:text;not null"`
	Source        string          `gorm:"type:text;not null"`
	Data          json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
}

func (EngagementEventRecord) TableName() string { return "engagement_events" }

// CandidatePlacementProfileRecord stores the combined qualifications +
// preferences summary used for agent guidance and vector matching.
// CV binary lives in the files service; candidate_profiles holds the file-id
// reference. Extracted CV text is folded into qualifications_text/summary_text.
type CandidatePlacementProfileRecord struct {
	CandidateID        string         `gorm:"primaryKey;type:text"`
	Version            int            `gorm:"not null;default:1"`
	SummaryText        string         `gorm:"type:text;not null;default:''"`
	QualificationsText string         `gorm:"type:text;not null;default:''"`
	PreferencesText    string         `gorm:"type:text;not null;default:''"`
	Missing            pq.StringArray `gorm:"type:text[];not null;default:'{}'"`
	Ready              bool           `gorm:"not null;default:false"`
	UpdatedAt          time.Time      `gorm:"not null;default:now()"`
}

func (CandidatePlacementProfileRecord) TableName() string { return "candidate_placement_profiles" }

// Schema returns the ordinary matching tables owned by GORM.
func Schema() []any {
	return []any{
		&CandidateMatchIndexRecord{},
		&CandidatePreferenceRecord{},
		&MatchRuleRecord{},
		&CandidateMatchRecord{},
		&CandidateMatchEventRecord{},
		&MatchRunEventRecord{},
		&EngagementEventRecord{},
		&CandidatePlacementProfileRecord{},
	}
}
