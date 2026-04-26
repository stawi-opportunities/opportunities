// Package v1 holds the Phase 5 event subscription handlers for
// apps/matching — cv-extract, cv-improve, cv-embed. Each implements
// Frame's events.EventI contract.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// CVExtractor abstracts extraction.Extractor.ExtractCV so tests can
// inject a deterministic fake.
type CVExtractor interface {
	ExtractCV(ctx context.Context, text string) (*extraction.CVFields, error)
}

// ScoreComponents mirrors cv.ScoreComponents but is redeclared here so
// the handler file's dependency graph stays shallow (cv.Scorer is the
// production implementer).
type ScoreComponents struct {
	ATS      int
	Keywords int
	Impact   int
	RoleFit  int
	Clarity  int
	Overall  int
}

// CVScorer abstracts cv.Scorer.Score.
type CVScorer interface {
	Score(ctx context.Context, cvText string, fields *extraction.CVFields, targetRole string) *ScoreComponents
}

// CVExtractDeps bundles collaborators.
type CVExtractDeps struct {
	Svc                   *frame.Service
	Extractor             CVExtractor
	Scorer                CVScorer
	ExtractorModelVersion string
	ScorerModelVersion    string
}

// CVExtractHandler consumes candidates.cv.uploaded.v1 and emits
// candidates.cv.extracted.v1.
type CVExtractHandler struct {
	deps CVExtractDeps
}

// NewCVExtractHandler wires the handler.
func NewCVExtractHandler(deps CVExtractDeps) *CVExtractHandler {
	return &CVExtractHandler{deps: deps}
}

func (h *CVExtractHandler) Name() string { return eventsv1.TopicCVUploaded }
func (h *CVExtractHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *CVExtractHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("cv-extract: empty payload")
	}
	return nil
}
func (h *CVExtractHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVUploadedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("cv-extract: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	if in.ExtractedText == "" {
		log.Warn("cv-extract: empty text; skipping")
		return nil
	}

	fields, err := h.deps.Extractor.ExtractCV(ctx, in.ExtractedText)
	if err != nil {
		return fmt.Errorf("cv-extract: ExtractCV: %w", err)
	}
	sc := h.deps.Scorer.Score(ctx, in.ExtractedText, fields, "")

	out := eventsv1.CVExtractedV1{
		CandidateID:         in.CandidateID,
		CVVersion:           in.CVVersion,
		Name:                fields.Name,
		Email:               fields.Email,
		Phone:               fields.Phone,
		Location:            fields.Location,
		CurrentTitle:        fields.CurrentTitle,
		Bio:                 fields.Bio,
		Seniority:           fields.Seniority,
		YearsExperience:     fields.YearsExperience,
		PrimaryIndustry:     fields.PrimaryIndustry,
		StrongSkills:        fields.StrongSkills,
		WorkingSkills:       fields.WorkingSkills,
		ToolsFrameworks:     fields.ToolsFrameworks,
		Certifications:      fields.Certifications,
		PreferredRoles:      fields.PreferredRoles,
		Languages:           fields.Languages,
		Education:           fields.Education,
		PreferredLocations:  fields.PreferredLocations,
		RemotePreference:    fields.RemotePreference,
		SalaryMin:           parseSalaryClamped(fields.SalaryMin),
		SalaryMax:           parseSalaryClamped(fields.SalaryMax),
		Currency:            fields.Currency,
		ScoreATS:            sc.ATS,
		ScoreKeywords:       sc.Keywords,
		ScoreImpact:         sc.Impact,
		ScoreRoleFit:        sc.RoleFit,
		ScoreClarity:        sc.Clarity,
		ScoreOverall:        sc.Overall,
		ModelVersionExtract: h.deps.ExtractorModelVersion,
		ModelVersionScore:   h.deps.ScorerModelVersion,
	}

	envOut := eventsv1.NewEnvelope(eventsv1.TopicCVExtracted, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCVExtracted, envOut); err != nil {
		return fmt.Errorf("cv-extract: emit: %w", err)
	}
	log.WithField("score_overall", out.ScoreOverall).Info("cv-extract: done")
	return nil
}

// parseSalaryClamped normalises extraction.CVFields' string salary
// fields (LLM output: "80000", "85k", "120,000") into the int form
// used by CVExtractedV1. Non-parseable values clamp to 0. Handles
// "k"/"K" suffix by multiplying by 1000. Strips commas and whitespace.
func parseSalaryClamped(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	lower := strings.ToLower(s)
	mult := 1
	if strings.HasSuffix(lower, "k") {
		mult = 1000
		lower = lower[:len(lower)-1]
	}
	// Strip commas and spaces.
	lower = strings.ReplaceAll(lower, ",", "")
	lower = strings.TrimSpace(lower)
	n, err := strconv.ParseFloat(lower, 64)
	if err != nil || n < 0 {
		return 0
	}
	return int(n * float64(mult))
}
