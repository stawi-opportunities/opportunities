package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
)

// OnboardingDraftReader returns the persisted draft as raw JSON, or
// `{}` when the candidate has none. Implemented by
// repository.CandidateRepository in production; the interface lets
// the handler test against an in-memory fake.
type OnboardingDraftReader interface {
	GetOnboardingDraft(ctx context.Context, candidateID string) ([]byte, error)
}

// OnboardingDraftWriter persists a raw JSON draft. The body is opaque
// here — the wizard owns the schema; we only assert outer-envelope
// shape (step in 1..3, presence of fields).
type OnboardingDraftWriter interface {
	SetOnboardingDraft(ctx context.Context, candidateID string, draft []byte) error
}

// OnboardingDraftStore bundles the two operations a single handler
// uses. Splitting the read and write interfaces lets each test
// depend on only what it exercises.
type OnboardingDraftStore interface {
	OnboardingDraftReader
	OnboardingDraftWriter
}

// OnboardingDeps bundles the inputs the handler needs.
type OnboardingDeps struct {
	Drafts OnboardingDraftStore
	// Placement / Profiles optional: rehydrate CV capabilities on GET so
	// resume never loses a past upload when draft.extra_info is thin.
	Placement *placement.Service
	Profiles  placement.ProfileStore
	// Now lets tests pin the server timestamp the handler embeds in
	// the draft envelope on Put. Defaults to time.Now in production.
	Now func() time.Time
}

func (d *OnboardingDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// onboardingEnvelope is the wire shape both endpoints use. Fields
// stays a raw JSON value so the wizard can evolve its schema without
// any backend code change. Messages holds the preference-chat transcript
// so conversations remain available across sessions and post-onboard refine.
type onboardingEnvelope struct {
	Step      int                     `json:"step"`
	Fields    json.RawMessage         `json:"fields"`
	Messages  []onboardingChatMessage `json:"messages,omitempty"`
	UpdatedAt *time.Time              `json:"updated_at,omitempty"`
}

// OnboardingHandler dispatches GET and PUT on the same path. Wrap
// with httpmw.CandidateAuth before mounting; the wrapper populates
// the candidate ID into the request context.
func OnboardingHandler(deps OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleOnboardingGet(deps, w, r)
		case http.MethodPut:
			handleOnboardingPut(deps, w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use GET to load draft, PUT to save")
		}
	}
}

func handleOnboardingGet(deps OnboardingDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	raw, err := deps.Drafts.GetOnboardingDraft(ctx, candidateID)
	if err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).
			Error("me/onboarding: draft lookup failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway,
			"draft_lookup_failed", "could not load onboarding draft")
		return
	}

	// Render the canonical default for an empty draft so the client
	// doesn't have to special-case `{}` vs a real envelope.
	out := onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &out); err != nil {
			// Stored draft is corrupt — treat as empty rather than
			// 500 the client. The next PUT will overwrite it.
			log.WithError(err).WithField("candidate_id", candidateID).
				Warn("me/onboarding: stored draft is malformed; returning default envelope")
			out = onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}
		}
	}

	// Rehydrate CV text into fields so the client doesn't re-demand upload.
	fields := fieldsFromEnvelope(out)
	fields = hydrateCapabilities(ctx, MeChatDeps{
		Placement: deps.Placement,
		Profiles:  deps.Profiles,
	}, candidateID, fields)
	if hasCapabilities(fields) {
		if b, mErr := json.Marshal(fields); mErr == nil {
			out.Fields = mergeRawFields(out.Fields, b)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleOnboardingPut(deps OnboardingDeps, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := util.Log(ctx)
	candidateID := httpmw.CandidateFromContext(ctx)

	// Larger limit: fields + multi-turn chat transcript.
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"body_read_failed", "could not read request body")
		return
	}
	var in onboardingEnvelope
	if err := json.Unmarshal(body, &in); err != nil {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"invalid_json", "request body is not valid JSON")
		return
	}
	if in.Step < 1 || in.Step > 3 {
		httpmw.ProblemJSON(w, http.StatusBadRequest,
			"invalid_step", "step must be 1, 2, or 3")
		return
	}
	if len(in.Fields) == 0 {
		in.Fields = json.RawMessage(`{}`)
	}

	// Load prior once for messages + monotonic step.
	prior, priorErr := loadOnboardingEnvelope(ctx, deps.Drafts, candidateID)
	if priorErr != nil {
		log.WithError(priorErr).WithField("candidate_id", candidateID).
			Warn("me/onboarding: prior load failed; continuing with request body")
	}
	// Never regress plan/payment stage — clients mid-chat may still POST step=1.
	if prior.Step > in.Step {
		in.Step = prior.Step
	}
	// Preserve stored chat when the client only updates fields/step.
	if len(in.Messages) == 0 {
		in.Messages = prior.Messages
	}
	in.Messages = clampChatMessages(in.Messages, 80)

	now := deps.now()
	in.UpdatedAt = &now
	out, err := json.Marshal(in)
	if err != nil {
		log.WithError(err).Error("me/onboarding: re-marshal failed (programmer error)")
		httpmw.ProblemJSON(w, http.StatusInternalServerError,
			"internal", "could not serialise draft envelope")
		return
	}
	if err := deps.Drafts.SetOnboardingDraft(ctx, candidateID, out); err != nil {
		log.WithError(err).WithField("candidate_id", candidateID).
			Error("me/onboarding: draft persist failed")
		httpmw.ProblemJSON(w, http.StatusBadGateway,
			"draft_persist_failed", "could not save onboarding draft")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadOnboardingEnvelope reads the stored draft, returning a default when empty.
func loadOnboardingEnvelope(ctx context.Context, drafts OnboardingDraftStore, candidateID string) (onboardingEnvelope, error) {
	out := onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}
	if drafts == nil {
		return out, nil
	}
	raw, err := drafts.GetOnboardingDraft(ctx, candidateID)
	if err != nil {
		return out, err
	}
	if len(raw) == 0 || string(raw) == "{}" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return onboardingEnvelope{Step: 1, Fields: json.RawMessage(`{}`)}, nil
	}
	if len(out.Fields) == 0 {
		out.Fields = json.RawMessage(`{}`)
	}
	return out, nil
}
