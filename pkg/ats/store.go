package ats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame/v2/data"
	"gorm.io/gorm"
)

// Store is the ATS persistence layer. All queries must filter tenant+partition.
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Migrate runs AutoMigrate for ATS schema.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.db.WithContext(ctx).AutoMigrate(Schema()...); err != nil {
		return fmt.Errorf("ats: migrate: %w", err)
	}
	return nil
}

func (s *Store) DB() *gorm.DB { return s.db }

// Create inserts model after GenID from claims context.
func (s *Store) Create(ctx context.Context, model data.BaseModelI) error {
	if m, ok := model.(interface{ GenID(context.Context) }); ok {
		m.GenID(ctx)
	}
	if err := s.db.WithContext(ctx).Create(model).Error; err != nil {
		return fmt.Errorf("ats: create: %w", err)
	}
	return nil
}

func (s *Store) Save(ctx context.Context, model any) error {
	if err := s.db.WithContext(ctx).Save(model).Error; err != nil {
		return fmt.Errorf("ats: save: %w", err)
	}
	return nil
}

// --- Jobs ---

func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	j.GenID(ctx)
	if j.Status == "" {
		j.Status = JobStatusDraft
	}
	if j.Visibility == "" {
		j.Visibility = VisibilityPrivate
	}
	if err := s.db.WithContext(ctx).Create(j).Error; err != nil {
		return fmt.Errorf("ats: create job: %w", err)
	}
	return nil
}

func (s *Store) GetJob(ctx context.Context, tenantID, partitionID, id string) (*Job, error) {
	var j Job
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get job: %w", err)
	}
	return &j, nil
}

func (s *Store) ListJobs(ctx context.Context, tenantID, partitionID string, status string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID).
		Order("created_at DESC").
		Limit(limit)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []Job
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("ats: list jobs: %w", err)
	}
	return out, nil
}

// --- Applications ---

func (s *Store) CreateApplication(ctx context.Context, a *Application) error {
	a.GenID(ctx)
	if a.Stage == "" {
		a.Stage = StageApplied
	}
	if a.Status == "" {
		a.Status = AppStatusActive
	}
	if a.Source == "" {
		a.Source = SourceManual
	}
	if err := s.db.WithContext(ctx).Create(a).Error; err != nil {
		return fmt.Errorf("ats: create application: %w", err)
	}
	return nil
}

func (s *Store) GetApplication(ctx context.Context, tenantID, partitionID, id string) (*Application, error) {
	var a Application
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get application: %w", err)
	}
	return &a, nil
}

func (s *Store) GetActiveApplication(ctx context.Context, tenantID, partitionID, jobID, profileID string) (*Application, error) {
	var a Application
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND job_id = ? AND profile_id = ? AND status = ?",
			tenantID, partitionID, jobID, profileID, AppStatusActive).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get active application: %w", err)
	}
	return &a, nil
}

func (s *Store) ListApplicationsByJob(ctx context.Context, tenantID, partitionID, jobID, stage string, limit int) ([]Application, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND job_id = ?", tenantID, partitionID, jobID).
		Order("created_at DESC").
		Limit(limit)
	if stage != "" {
		q = q.Where("stage = ?", stage)
	}
	var out []Application
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("ats: list applications: %w", err)
	}
	return out, nil
}

func (s *Store) AppendStageEvent(ctx context.Context, e *StageEvent) error {
	e.GenID(ctx)
	if err := s.db.WithContext(ctx).Create(e).Error; err != nil {
		return fmt.Errorf("ats: stage event: %w", err)
	}
	return nil
}

// --- Availability ---

func (s *Store) UpsertAvailability(ctx context.Context, a *Availability) error {
	var existing Availability
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND profile_id = ?",
			a.TenantID, a.PartitionID, a.ProfileID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		a.GenID(ctx)
		return s.db.WithContext(ctx).Create(a).Error
	}
	if err != nil {
		return fmt.Errorf("ats: get availability: %w", err)
	}
	existing.Timezone = a.Timezone
	existing.RulesJSON = a.RulesJSON
	existing.ExceptionsJSON = a.ExceptionsJSON
	existing.ModifiedAt = time.Now().UTC()
	if err := s.db.WithContext(ctx).Save(&existing).Error; err != nil {
		return fmt.Errorf("ats: update availability: %w", err)
	}
	*a = existing
	return nil
}

func (s *Store) GetAvailability(ctx context.Context, tenantID, partitionID, profileID string) (*Availability, error) {
	var a Availability
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND profile_id = ?", tenantID, partitionID, profileID).
		First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get availability: %w", err)
	}
	return &a, nil
}

// --- Interviews ---

func (s *Store) CreateInterview(ctx context.Context, iv *Interview) error {
	iv.GenID(ctx)
	if iv.Status == "" {
		iv.Status = InterviewProposed
	}
	if err := s.db.WithContext(ctx).Create(iv).Error; err != nil {
		return fmt.Errorf("ats: create interview: %w", err)
	}
	return nil
}

func (s *Store) GetInterview(ctx context.Context, tenantID, partitionID, id string) (*Interview, error) {
	var iv Interview
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND partition_id = ?", id, tenantID, partitionID).
		First(&iv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ats: get interview: %w", err)
	}
	return &iv, nil
}

func (s *Store) ListScheduledBusy(ctx context.Context, tenantID, partitionID string, panel []string, from, to time.Time) ([]BusyInterval, error) {
	var rows []Interview
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start IS NOT NULL AND slot_end IS NOT NULL AND slot_start < ? AND slot_end > ?",
			tenantID, partitionID, InterviewScheduled, to, from).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list busy: %w", err)
	}
	var busy []BusyInterval
	for _, r := range rows {
		if r.SlotStart == nil || r.SlotEnd == nil {
			continue
		}
		// If panel filter non-empty, only count interviews that share a panelist.
		if len(panel) > 0 && !panelOverlapsJSON(r.PanelJSON, panel) {
			continue
		}
		busy = append(busy, BusyInterval{Start: *r.SlotStart, End: *r.SlotEnd})
	}
	return busy, nil
}

func panelOverlapsJSON(panelJSON string, panel []string) bool {
	// Cheap substring check sufficient for id lists stored as JSON arrays of quoted ids.
	for _, p := range panel {
		if p != "" && containsProfile(panelJSON, p) {
			return true
		}
	}
	return false
}

func containsProfile(panelJSON, profileID string) bool {
	if profileID == "" {
		return false
	}
	return strings.Contains(panelJSON, `"`+profileID+`"`)
}

// --- Hire outcome ---

func (s *Store) GetHireOutcomeByApp(ctx context.Context, applicationID string) (*HireOutcome, error) {
	var h HireOutcome
	err := s.db.WithContext(ctx).Where("application_id = ?", applicationID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func (s *Store) CreateHireOutcome(ctx context.Context, h *HireOutcome) error {
	h.GenID(ctx)
	if err := s.db.WithContext(ctx).Create(h).Error; err != nil {
		return fmt.Errorf("ats: hire outcome: %w", err)
	}
	return nil
}

// --- Outbox ---

func (s *Store) CreateOutbox(ctx context.Context, m *OutboxMessage) error {
	m.GenID(ctx)
	if m.Status == "" {
		m.Status = "pending"
	}
	if err := s.db.WithContext(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("ats: outbox: %w", err)
	}
	return nil
}

// ListUpcomingInterviews returns scheduled interviews in [from, to) for a partition.
func (s *Store) ListUpcomingInterviews(ctx context.Context, tenantID, partitionID string, from, to time.Time, limit int) ([]Interview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []Interview
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start IS NOT NULL AND slot_start >= ? AND slot_start < ?",
			tenantID, partitionID, InterviewScheduled, from, to).
		Order("slot_start ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list upcoming interviews: %w", err)
	}
	return rows, nil
}

// ListInterviewsByApplication lists interviews for one application.
func (s *Store) ListInterviewsByApplication(ctx context.Context, tenantID, partitionID, applicationID string) ([]Interview, error) {
	var rows []Interview
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND application_id = ?", tenantID, partitionID, applicationID).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list interviews: %w", err)
	}
	return rows, nil
}

// CountJobs counts jobs by optional status.
func (s *Store) CountJobs(ctx context.Context, tenantID, partitionID, status string) (int64, error) {
	q := s.db.WithContext(ctx).Model(&Job{}).Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountApplications counts applications by optional status.
func (s *Store) CountApplications(ctx context.Context, tenantID, partitionID, status string) (int64, error) {
	q := s.db.WithContext(ctx).Model(&Application{}).Where("tenant_id = ? AND partition_id = ?", tenantID, partitionID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountInterviewsInRange counts scheduled interviews.
func (s *Store) CountInterviewsInRange(ctx context.Context, tenantID, partitionID string, from, to time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Interview{}).
		Where("tenant_id = ? AND partition_id = ? AND status = ? AND slot_start >= ? AND slot_start < ?",
			tenantID, partitionID, InterviewScheduled, from, to).
		Count(&n).Error
	return n, err
}

// ListApplicationsForProfile returns applications where profile is the candidate (candidate portal).
func (s *Store) ListApplicationsForProfile(ctx context.Context, tenantID, partitionID, profileID string) ([]Application, error) {
	var rows []Application
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND partition_id = ? AND profile_id = ?", tenantID, partitionID, profileID).
		Order("created_at DESC").
		Limit(100).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("ats: list apps for profile: %w", err)
	}
	return rows, nil
}

// GetInterviewForCandidate ensures interview's application belongs to profileID.
func (s *Store) GetInterviewForCandidate(ctx context.Context, tenantID, partitionID, interviewID, profileID string) (*Interview, *Application, error) {
	iv, err := s.GetInterview(ctx, tenantID, partitionID, interviewID)
	if err != nil || iv == nil {
		return nil, nil, err
	}
	a, err := s.GetApplication(ctx, tenantID, partitionID, iv.ApplicationID)
	if err != nil || a == nil {
		return nil, nil, err
	}
	if a.ProfileID != profileID {
		return nil, nil, nil
	}
	return iv, a, nil
}
