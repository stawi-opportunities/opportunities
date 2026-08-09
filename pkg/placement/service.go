package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
)

// Embedder produces a vector for placement summary text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Service rebuilds placement profiles and refreshes the match index vector.
// CV binaries live in the files service; this layer only holds the summary.
type Service struct {
	Store Store
	// Files optional: used only by HTTP upload (not by Rebuild itself).
	Files FileStore
	// Profiles optional: persist file-id reference on candidate_profiles.
	Profiles ProfileStore
	Embedder Embedder
	// Index optional: patch filters + embedding when available.
	Index *matching.IndexStore
	// Svc + EmbedQueue publish CandidateEmbeddingV1 for gap-fill consumers.
	Svc        *frame.Service
	EmbedQueue string
	// ModelVersion stamped on embedding events.
	ModelVersion string
}

// RebuildInput carries the latest chat fields; CV text is merged from CV store
// when ExtraInfo is empty or shorter. ChatTurns drive conversation digest.
type RebuildInput struct {
	CandidateID string
	Fields      Fields
	// ChatTurns optional transcript for conversation-grounded intent.
	ChatTurns []ChatTurn
	// Persona configures section budgets (nil → DefaultPersonaConfig).
	Persona *PersonaConfig
	// StrictEmbed makes embed/index failures return an error instead of
	// soft-succeeding with Embedded=false. Use for on-demand match refresh
	// so callers do not hide system failures as "no CV".
	StrictEmbed bool
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
	// Prefer a full prior CV when the turn only has thin ExtraInfo.
	if !looksLikeCV(fields.ExtraInfo) && s.Store != nil {
		if prior, err := s.Store.Get(ctx, in.CandidateID); err == nil && prior != nil {
			priorCV := stripQualHeader(prior.QualificationsText)
			priorCV = strings.TrimSpace(priorCV)
			if priorCV == "(CV not yet provided)" {
				priorCV = ""
			}
			if looksLikeCV(priorCV) || (strings.TrimSpace(fields.ExtraInfo) == "" && priorCV != "") {
				fields.ExtraInfo = priorCV
			}
		}
	}

	cfg := DefaultPersonaConfig()
	if in.Persona != nil {
		cfg = *in.Persona
	}
	// Preserve prior conversation digest when this rebuild has no chat turns
	// (e.g. CV-only refresh) so intent is not dropped.
	turns := in.ChatTurns
	if len(turns) == 0 && s.Store != nil {
		if prior, err := s.Store.Get(ctx, in.CandidateID); err == nil && prior != nil &&
			strings.TrimSpace(prior.ConversationDigest) != "" {
			// Synthetic user turn carrying prior digest for BuildConversationDigest skip —
			// inject digest after build instead.
			doc := BuildPersonaDocument(in.CandidateID, fields, nil, cfg)
			doc.ConversationDigest = prior.ConversationDigest
			doc.SummaryText = composePersonaSummary(doc.QualificationsText, doc.PreferencesText, prior.ConversationDigest, fields, doc.Missing)
			doc.SummaryText = truncateRunes(doc.SummaryText, cfg.MaxRunes)
			doc.RerankText = RerankText(matchHeadline(fields), doc.PreferencesText, prior.ConversationDigest, 1800)
			doc.ContentHash = ContentHash(doc.SummaryText)
			return s.finishRebuild(ctx, in, fields, doc, cfg)
		}
	}
	doc := BuildPersonaDocument(in.CandidateID, fields, turns, cfg)
	return s.finishRebuild(ctx, in, fields, doc, cfg)
}

func (s *Service) finishRebuild(ctx context.Context, in RebuildInput, fields Fields, doc Document, cfg PersonaConfig) (*RebuildResult, error) {
	candidateID := in.CandidateID
	if doc.CandidateID == "" {
		doc.CandidateID = candidateID
	}

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
		if err := s.Index.UpsertFilters(ctx, candidateID, matching.IndexFilters{
			Countries:      filters.Countries,
			Kinds:          filters.Kinds,
			SalaryFloorUSD: filters.SalaryFloorUSD,
			RemoteOnly:     filters.RemoteOnly,
		}); err != nil && err != matching.ErrNotFound {
			util.Log(ctx).WithError(err).WithField("candidate_id", candidateID).
				Debug("placement: filter upsert skipped")
		}
	}

	embedded := false
	// Embed when we have any material for matching (role, CV/skills text, or intent).
	// Thin ExtraInfo still gets a vector so refresh is not stuck on "no embedding".
	hasSignal := strings.TrimSpace(fields.TargetJobTitle) != "" ||
		strings.TrimSpace(fields.ExtraInfo) != "" ||
		strings.TrimSpace(doc.ConversationDigest) != ""
	if !hasSignal {
		if in.StrictEmbed {
			return nil, fmt.Errorf("placement: nothing to embed for candidate %s", candidateID)
		}
		return &RebuildResult{Document: doc, Version: version, Embedded: false}, nil
	}
	if s.Embedder == nil {
		if in.StrictEmbed {
			return nil, fmt.Errorf("placement: embedder not configured")
		}
		return &RebuildResult{Document: doc, Version: version, Embedded: false}, nil
	}

	text := extraction.EmbedQueryPrefix + doc.SummaryText
	text = truncateRunes(text, cfg.MaxRunes)
	// Skip re-embed when index already has this persona (stable rerank text).
	skipEmbed := false
	if s.Index != nil {
		if existing, gErr := s.Index.Get(ctx, candidateID); gErr == nil && existing != nil &&
			len(existing.Embedding) > 0 && existing.RerankText != "" &&
			existing.RerankText == doc.RerankText {
			skipEmbed = true
			embedded = true
		}
	}
	var vec []float32
	var err error
	if !skipEmbed {
		vec, err = s.Embedder.Embed(ctx, text)
		if err != nil {
			if in.StrictEmbed {
				return nil, fmt.Errorf("placement: embed failed: %w", err)
			}
			util.Log(ctx).WithError(err).WithField("candidate_id", candidateID).
				Warn("placement: embed failed (summary still stored)")
		}
	} else if s.Index != nil {
		if existing, gErr := s.Index.Get(ctx, candidateID); gErr == nil && existing != nil {
			vec = existing.Embedding
		}
	}
	if err == nil && len(vec) > 0 {
		embedded = true
		if s.Index != nil {
			// Free-proof-safe defaults until ActivateSubscription rewrites caps.
			entDaily, entWeekly := 1, 3
			ci := matching.CandidateIndex{
				CandidateID:    candidateID,
				Embedding:      vec,
				MinScore:       0.45,
				DailyCap:       entDaily,
				WeeklyCap:      entWeekly,
				Kinds:          filters.Kinds,
				Countries:      filters.Countries,
				SalaryFloorUSD: filters.SalaryFloorUSD,
				RemoteOnly:     filters.RemoteOnly,
				RerankText:     doc.RerankText,
				Enabled:        true,
			}
			if existing, gErr := s.Index.Get(ctx, candidateID); gErr == nil && existing != nil {
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
				if in.StrictEmbed {
					return nil, fmt.Errorf("placement: match index upsert failed: %w", uErr)
				}
				util.Log(ctx).WithError(uErr).WithField("candidate_id", candidateID).
					Warn("placement: match index upsert failed")
			}
		}
		// Path C gap-fill; source=persona protects against thin CV overwrites.
		if !skipEmbed && s.Svc != nil && s.EmbedQueue != "" {
			out := eventsv1.CandidateEmbeddingV1{
				CandidateID:  candidateID,
				CVVersion:    version,
				Vector:       vec,
				ModelVersion: s.ModelVersion,
				Source:       eventsv1.EmbeddingSourcePersona,
			}
			env := eventsv1.NewEnvelope(eventsv1.TopicCandidateEmbedding, out)
			body, mErr := json.Marshal(env)
			if mErr == nil {
				if pErr := s.Svc.QueueManager().Publish(ctx, s.EmbedQueue, body, nil); pErr != nil {
					util.Log(ctx).WithError(pErr).WithField("candidate_id", candidateID).
						Warn("placement: publish embedding event failed")
				}
			}
		}
	}

	if in.StrictEmbed && !embedded {
		return nil, fmt.Errorf("placement: embedding not available after rebuild")
	}

	return &RebuildResult{Document: doc, Version: version, Embedded: embedded}, nil
}

func stripQualHeader(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "## Qualifications")
	return strings.TrimSpace(s)
}
