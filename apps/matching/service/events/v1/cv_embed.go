package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

// Embedder abstracts extraction.Extractor.Embed.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// CVEmbedDeps bundles collaborators.
type CVEmbedDeps struct {
	Svc          *frame.Service
	Embedder     Embedder
	ModelVersion string
}

// CVEmbedHandler consumes the cv-embed queue subject and emits a
// CandidateEmbeddingV1 event onto the events bus.
//
// The input is an extracted payload; the handler composes an embedding
// text from the stable CV fields (name, bio, skills, roles) and sends
// it to the embedder. Errors propagate so Frame redelivers — an embed
// outage must not silently drop the embedding.
type CVEmbedHandler struct {
	deps CVEmbedDeps
}

func NewCVEmbedHandler(deps CVEmbedDeps) *CVEmbedHandler {
	return &CVEmbedHandler{deps: deps}
}

// Handle implements queue.SubscribeWorker.
func (h *CVEmbedHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("cv-embed: empty payload")
	}
	var env eventsv1.Envelope[eventsv1.CVExtractedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("cv-embed: decode: %w", err)
	}
	in := env.Payload

	log := util.Log(ctx).WithField("candidate_id", in.CandidateID).WithField("cv_version", in.CVVersion)

	text := composeEmbeddingText(&in)
	if text == "" {
		log.Warn("cv-embed: empty composed text; skipping")
		return nil
	}

	vec, err := h.deps.Embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("cv-embed: Embed: %w", err)
	}
	if len(vec) == 0 {
		log.Warn("cv-embed: embedder returned empty vector; skipping")
		return nil
	}

	out := eventsv1.CandidateEmbeddingV1{
		CandidateID:  in.CandidateID,
		CVVersion:    in.CVVersion,
		Vector:       vec,
		ModelVersion: h.deps.ModelVersion,
	}
	envOut := eventsv1.NewEnvelope(eventsv1.TopicCandidateEmbedding, out)
	if err := h.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateEmbedding, envOut); err != nil {
		return fmt.Errorf("cv-embed: emit: %w", err)
	}
	log.WithField("dim", len(vec)).Info("cv-embed: done")
	return nil
}

// composeEmbeddingText builds a stable string from the CV fields that
// captures what you'd search against for match scoring. Keep the
// composition deterministic so the same CV always embeds the same way.
func composeEmbeddingText(p *eventsv1.CVExtractedV1) string {
	var parts []string
	if p.CurrentTitle != "" {
		parts = append(parts, p.CurrentTitle)
	}
	if p.Seniority != "" {
		parts = append(parts, p.Seniority)
	}
	if p.PrimaryIndustry != "" {
		parts = append(parts, p.PrimaryIndustry)
	}
	if len(p.StrongSkills) > 0 {
		parts = append(parts, "skills: "+strings.Join(p.StrongSkills, ", "))
	}
	if len(p.PreferredRoles) > 0 {
		parts = append(parts, "roles: "+strings.Join(p.PreferredRoles, ", "))
	}
	if p.Bio != "" {
		parts = append(parts, p.Bio)
	}
	return strings.Join(parts, ". ")
}
