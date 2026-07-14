package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/cvstore"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// Embedder produces a vector for placement summary text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// CVSource loads the latest CV plain text for a candidate.
type CVSource interface {
	GetCurrent(ctx context.Context, candidateID string) (*cvstore.Document, error)
}

// Service rebuilds placement profiles and refreshes the match index vector.
type Service struct {
	Store    Store
	CV       CVSource
	Embedder Embedder
	// Index optional: patch filters + embedding when available.
	Index *matching.IndexStore
	// Svc + EmbedQueue publish CandidateEmbeddingV1 for gap-fill consumers.
	Svc        *frame.Service
	EmbedQueue string // eventsv1.TopicCandidateEmbedding or queue name
	// ModelVersion stamped on embedding events.
	ModelVersion string
}

// RebuildInput carries the latest chat fields; CV text is merged from CV store
// when ExtraInfo is empty or shorter.
type RebuildInput struct {
	CandidateID string
	Fields      Fields
}

// RebuildResult is returned for API surfaces (chat response).
type RebuildResult struct {
	Document Document
	Version  int
	Embedded bool
}

// Rebuild composes qualifications + preferences, persists the summary,
// optionally embeds for vector matching, and patches match-index filters.
func (s *Service) Rebuild(ctx context.Context, in RebuildInput) (*RebuildResult, error) {
	if s == nil {
		return nil, fmt.Errorf("placement: service is nil")
	}
	if in.CandidateID == "" {
		return nil, fmt.Errorf("placement: candidate_id required")
	}
	fields := in.Fields
	// Prefer longer CV from local document index.
	if s.CV != nil {
		if doc, err := s.CV.GetCurrent(ctx, in.CandidateID); err == nil && doc != nil {
			if looksLikeCV(doc.ExtractedText) {
				if !looksLikeCV(fields.ExtraInfo) || len(doc.ExtractedText) > len(fields.ExtraInfo) {
					fields.ExtraInfo = doc.ExtractedText
				}
			}
		}
	}

	doc := BuildDocument(in.CandidateID, fields)
	version := 1
	if s.Store != nil {
		v, err := s.Store.Upsert(ctx, doc)
		if err != nil {
			return nil, err
		}
		version = v
		doc.Version = v
	}

	// Always try to keep match-index filters in sync with preferences.
	filters := FiltersFromFields(fields)
	if s.Index != nil {
		if err := s.Index.UpsertFilters(ctx, in.CandidateID, matching.IndexFilters{
			Countries:      filters.Countries,
			Kinds:          filters.Kinds,
			SalaryFloorUSD: filters.SalaryFloorUSD,
			RemoteOnly:     filters.RemoteOnly,
		}); err != nil && err != matching.ErrNotFound {
			util.Log(ctx).WithError(err).WithField("candidate_id", in.CandidateID).
				Debug("placement: filter upsert skipped")
		}
	}

	embedded := false
	// Embed when we have meaningful signal (at least a role or a CV).
	if s.Embedder != nil && (strings.TrimSpace(fields.TargetJobTitle) != "" || looksLikeCV(fields.ExtraInfo)) {
		text := extraction.EmbedQueryPrefix + doc.SummaryText
		// Token-ish budget: keep under ~512-ish words for e5 models.
		text = truncateRunes(text, 3500)
		vec, err := s.Embedder.Embed(ctx, text)
		if err != nil {
			util.Log(ctx).WithError(err).WithField("candidate_id", in.CandidateID).
				Warn("placement: embed failed (summary still stored)")
		} else if len(vec) > 0 {
			embedded = true
			if s.Index != nil {
				entDaily, entWeekly := 25, 100
				ci := matching.CandidateIndex{
					CandidateID:    in.CandidateID,
					Embedding:      vec,
					MinScore:       0.5,
					DailyCap:       entDaily,
					WeeklyCap:      entWeekly,
					Kinds:          filters.Kinds,
					Countries:      filters.Countries,
					SalaryFloorUSD: filters.SalaryFloorUSD,
					RemoteOnly:     filters.RemoteOnly,
					Enabled:        true,
				}
				if existing, gErr := s.Index.Get(ctx, in.CandidateID); gErr == nil && existing != nil {
					ci.MinScore = existing.MinScore
					if existing.DailyCap > 0 {
						ci.DailyCap = existing.DailyCap
					}
					ci.WeeklyCap = existing.WeeklyCap
					if len(filters.Kinds) == 0 {
						ci.Kinds = existing.Kinds
					}
					if len(filters.Countries) == 0 {
						ci.Countries = existing.Countries
					}
					if filters.SalaryFloorUSD == nil {
						ci.SalaryFloorUSD = existing.SalaryFloorUSD
					}
				}
				if uErr := s.Index.Upsert(ctx, ci); uErr != nil {
					util.Log(ctx).WithError(uErr).WithField("candidate_id", in.CandidateID).
						Warn("placement: match index upsert failed")
				}
			}
			// Fan-out gap-fill via existing candidate-embedding consumer.
			if s.Svc != nil && s.EmbedQueue != "" {
				out := eventsv1.CandidateEmbeddingV1{
					CandidateID:  in.CandidateID,
					CVVersion:    version,
					Vector:       vec,
					ModelVersion: s.ModelVersion,
				}
				env := eventsv1.NewEnvelope(eventsv1.TopicCandidateEmbedding, out)
				body, mErr := json.Marshal(env)
				if mErr == nil {
					if pErr := s.Svc.QueueManager().Publish(ctx, s.EmbedQueue, body, nil); pErr != nil {
						util.Log(ctx).WithError(pErr).WithField("candidate_id", in.CandidateID).
							Warn("placement: publish embedding event failed")
					}
				}
			}
		}
	}

	return &RebuildResult{Document: doc, Version: version, Embedded: embedded}, nil
}
