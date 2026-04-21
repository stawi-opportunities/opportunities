package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "stawi.jobs/pkg/events/v1"
)

// PriorityFix mirrors cv.PriorityFix.
type PriorityFix struct {
	FixID          string
	Title          string
	ImpactLevel    string
	Category       string
	Why            string
	AutoApplicable bool
	Rewrite        string
}

// FixGenerator abstracts the combined detectPriorityFixes + AttachRewrites
// pipeline from pkg/cv. Production wiring passes an adapter that calls
// both in sequence; tests pass a hard-coded fake.
type FixGenerator interface {
	Generate(ctx context.Context, extracted *eventsv1.CVExtractedV1) ([]PriorityFix, error)
}

// CVImproveDeps bundles collaborators.
type CVImproveDeps struct {
	Svc          *frame.Service
	Fixes        FixGenerator
	ModelVersion string
}

// CVImproveHandler consumes candidates.cv.extracted.v1 and emits
// candidates.cv.improved.v1.
type CVImproveHandler struct {
	deps CVImproveDeps
}

func NewCVImproveHandler(deps CVImproveDeps) *CVImproveHandler {
	return &CVImproveHandler{deps: deps}
}

func (h *CVImproveHandler) Name() string { return eventsv1.TopicCVExtracted }
func (h *CVImproveHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}
func (h *CVImproveHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("cv-improve: empty payload")
	}
	return nil
}
func (h *CVImproveHandler) Execute(ctx context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return fmt.Errorf("cv-improve: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	fixes, err := h.deps.Fixes.Generate(ctx, &in)
	if err != nil {
		// Don't fail hard — an AI rewrite outage shouldn't block the
		// pipeline. Emit an empty-fixes event so downstream sees a
		// version bump + audit row.
		log.WithError(err).Warn("cv-improve: fix generation failed, emitting empty")
		fixes = nil
	}

	wire := make([]eventsv1.CVFix, 0, len(fixes))
	for _, f := range fixes {
		wire = append(wire, eventsv1.CVFix{
			FixID:          f.FixID,
			Title:          f.Title,
			ImpactLevel:    f.ImpactLevel,
			Category:       f.Category,
			Why:            f.Why,
			AutoApplicable: f.AutoApplicable,
			Rewrite:        f.Rewrite,
		})
	}

	out := eventsv1.CVImprovedV1{
		CandidateID:  in.CandidateID,
		CVVersion:    in.CVVersion,
		Fixes:        wire,
		ModelVersion: h.deps.ModelVersion,
	}
	envOut := eventsv1.NewEnvelope(eventsv1.TopicCVImproved, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCVImproved, envOut); err != nil {
		return fmt.Errorf("cv-improve: emit: %w", err)
	}
	log.WithField("fixes", len(wire)).Info("cv-improve: done")
	return nil
}
