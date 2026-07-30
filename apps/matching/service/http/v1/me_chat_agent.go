package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/chatagentclient"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
)

// opportunityChatContext is optional job-in-view context for side-chat.
type opportunityChatContext struct {
	ID          string `json:"id,omitempty"`
	Slug        string `json:"slug,omitempty"`
	Title       string `json:"title,omitempty"`
	Entity      string `json:"issuing_entity,omitempty"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
	ApplyURL    string `json:"apply_url,omitempty"`
}

// agentChatRequest extends the shared turn body with optional opportunity context.
type agentChatRequest struct {
	onboardingChatRequest
	// Context selects product mode: "placement" (default) or "opportunity".
	Context     string                  `json:"context,omitempty"`
	Opportunity *opportunityChatContext `json:"opportunity,omitempty"`
}

// sessionStore is an in-process cache of chat-agent session ids per
// (candidate, context_key, opportunity_slug). Durable state lives in chat-agent.
type sessionStore struct {
	mu    sync.Mutex
	byKey map[string]string
}

func (s *sessionStore) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[key]
}

func (s *sessionStore) set(key, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey == nil {
		s.byKey = map[string]string{}
	}
	s.byKey[key] = sessionID
}

// MeChatAgentDeps wires the platform chat-agent consumer path.
type MeChatAgentDeps struct {
	Client    *chatagentclient.Client
	Drafts    OnboardingDraftStore
	Placement *placement.Service
	Profiles  placement.ProfileStore
	// Sessions caches session ids (optional; nil uses ephemeral per request create).
	Sessions *sessionStore
	// EnsureContexts registers product contexts once (best-effort).
	ensureOnce sync.Once
}

// MeChatAgentHandler is POST /me/chat when CHAT_AGENT_ENABLED.
// SPA contract matches MeChatHandler; opportunity side-chat passes opportunity{}.
func MeChatAgentHandler(deps MeChatAgentDeps, fallback http.HandlerFunc) http.HandlerFunc {
	if deps.Client == nil {
		return fallback
	}
	if deps.Sessions == nil {
		deps.Sessions = &sessionStore{byKey: map[string]string{}}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "body_read_failed", "could not read request body")
			return
		}
		var in agentChatRequest
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
			return
		}

		msg := strings.TrimSpace(in.Message)
		cvText := strings.TrimSpace(in.CVText)
		if msg == "" && cvText == "" && strings.TrimSpace(in.LinkedIn) == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest, "empty_message", "message, linkedin, or cv_text is required")
			return
		}
		if msg == "" {
			switch {
			case cvText != "":
				name := strings.TrimSpace(in.CVFilename)
				if name == "" {
					name = "CV"
				}
				msg = "I've attached my CV (" + name + ") for matching."
			case strings.TrimSpace(in.LinkedIn) != "":
				msg = "My LinkedIn is " + strings.TrimSpace(in.LinkedIn)
			}
		}

		// Best-effort register product contexts once per process.
		deps.ensureOnce.Do(func() {
			for _, def := range []chatagentclient.ContextDefinition{
				chatagentclient.PlacementIntakeContext(),
				chatagentclient.OpportunityViewContext(),
			} {
				if _, err := deps.Client.UpsertContext(ctx, def); err != nil {
					log.WithError(err).WithField("context_key", def.ContextKey).
						Warn("me/chat: ensure context failed (will use inline_config)")
				}
			}
		})

		mode := strings.ToLower(strings.TrimSpace(in.Context))
		if mode == "" {
			if in.Opportunity != nil && (in.Opportunity.Slug != "" || in.Opportunity.Title != "") {
				mode = "opportunity"
			} else {
				mode = "placement"
			}
		}

		contextKey := chatagentclient.ContextPlacementIntake
		inline := chatagentclient.PlacementIntakeContext()
		if mode == "opportunity" {
			contextKey = chatagentclient.ContextOpportunityView
			inline = chatagentclient.OpportunityViewContext()
		}

		// Load prior draft for seed fields + continuity.
		stored, _ := loadOnboardingEnvelope(ctx, deps.Drafts, candidateID)
		priorFields := fieldsFromEnvelope(stored)
		priorFields = hydrateCapabilities(ctx, MeChatDeps{
			Drafts: deps.Drafts, Placement: deps.Placement, Profiles: deps.Profiles,
		}, candidateID, priorFields)
		seed := fieldsToAgentMap(priorFields)
		// Overlay client draft.
		seed = mergeAgentMaps(seed, fieldsToAgentMap(sanitizeFields(in.Draft)))

		var docs []chatagentclient.DocumentEvidence
		if cvText != "" {
			docs = append(docs, chatagentclient.DocumentEvidence{
				Name: "capabilities", Kind: "cv", Text: truncateRunes(cvText, 80_000),
			})
		} else if s := strings.TrimSpace(priorFields.ExtraInfo); s != "" {
			docs = append(docs, chatagentclient.DocumentEvidence{
				Name: "capabilities", Kind: "cv", Text: truncateRunes(s, 80_000),
			})
		}

		runtime := map[string]any{}
		if mode == "opportunity" && in.Opportunity != nil {
			op := in.Opportunity
			runtime["opportunity_id"] = op.ID
			runtime["opportunity_slug"] = op.Slug
			runtime["opportunity_title"] = op.Title
			runtime["opportunity_entity"] = op.Entity
			runtime["opportunity_location"] = op.Location
			runtime["opportunity_kind"] = op.Kind
			if d := strings.TrimSpace(op.Description); d != "" {
				docs = append(docs, chatagentclient.DocumentEvidence{
					Name: "opportunity", Kind: "listing", Text: truncateRunes(d, 12_000),
				})
			}
			// Soft prompt the model with page context without polluting user bubble.
			msg = fmt.Sprintf(
				"[Viewing opportunity: %q at %s%s. slug=%s]\n\n%s",
				op.Title, op.Entity,
				func() string {
					if op.Location == "" {
						return ""
					}
					return ", " + op.Location
				}(),
				op.Slug, msg,
			)
		}

		sessionKey := candidateID + "|" + contextKey
		if mode == "opportunity" && in.Opportunity != nil {
			sessionKey += "|" + in.Opportunity.Slug
		}

		sessionID := deps.Sessions.get(sessionKey)
		if sessionID == "" {
			sess, cerr := deps.Client.CreateSession(ctx, chatagentclient.CreateSessionRequest{
				SubjectID:        candidateID,
				ContextKey:       contextKey,
				InlineConfig:     &inline,
				SeedFields:       seed,
				Documents:        docs,
				SeedMessages:     historyToAgent(stored.Messages),
				Runtime:          runtime,
				EvaluateEvidence: len(docs) > 0 || len(seed) > 0,
			})
			if cerr != nil {
				log.WithError(cerr).Warn("me/chat: CreateSession failed; falling back to local handler")
				// Rebuild body for fallback.
				r.Body = io.NopCloser(bytesReader(body))
				fallback(w, r)
				return
			}
			sessionID = sess.ID
			deps.Sessions.set(sessionKey, sessionID)
		}

		structured := map[string]any{}
		if li := normalizeLinkedIn(in.LinkedIn); li != "" {
			structured["linkedin"] = li
		}

		turnDocs := docs
		// Only send new CV on turns that include upload; session already has seed docs.
		if cvText == "" {
			turnDocs = nil
		}

		tres, terr := deps.Client.Turn(ctx, chatagentclient.TurnRequest{
			SessionID:  sessionID,
			Message:    msg,
			Structured: structured,
			Documents:  turnDocs,
		})
		if terr != nil {
			log.WithError(terr).Warn("me/chat: Turn failed; falling back to local handler")
			// Drop bad session cache so next request recreates.
			deps.Sessions.set(sessionKey, "")
			r.Body = io.NopCloser(bytesReader(body))
			fallback(w, r)
			return
		}

		fields := agentMapToFields(tres.Session.Fields)
		fields = sanitizeFields(fields)
		fields = applySafeDefaults(fields)
		// Composer CV wins.
		if cvText != "" && len(cvText) >= len(strings.TrimSpace(fields.ExtraInfo)) {
			fields.ExtraInfo = truncateRunes(cvText, 8000)
		}

		status := assessFieldStatus(fields)
		missing := missingFromStatus(status)
		ready := len(missing) == 0
		reply := composeReply(tres.Reply, fields, missing, ready)

		// Map agent messages to SPA shape.
		var messages []onboardingChatMessage
		if tres.Session != nil {
			for _, m := range tres.Session.Messages {
				messages = append(messages, onboardingChatMessage{Role: m.Role, Content: m.Content})
			}
		}
		if len(messages) == 0 {
			messages = appendChatTurn(sanitizeHistory(in.History), msg, reply)
		}

		// Persist placement draft for resume across pages (SPA /me/onboarding).
		if deps.Drafts != nil && candidateID != "" {
			if err := persistChatSession(ctx, MeChatDeps{Drafts: deps.Drafts}, candidateID, stored, fields, messages, ready); err != nil {
				log.WithError(err).Warn("me/chat: draft persist failed")
			}
		}

		// Placement rebuild remains matching's responsibility.
		var placementSummary string
		placementReady := ready
		if deps.Placement != nil && candidateID != "" {
			turns := make([]placement.ChatTurn, 0, len(messages))
			for _, m := range messages {
				turns = append(turns, placement.ChatTurn{Role: m.Role, Content: m.Content})
			}
			res, pErr := deps.Placement.Rebuild(ctx, placement.RebuildInput{
				CandidateID: candidateID,
				Fields:      toPlacementFields(fields),
				ChatTurns:   turns,
			})
			if pErr != nil {
				log.WithError(pErr).Warn("me/chat: placement rebuild failed")
			} else if res != nil {
				placementSummary = res.Document.SummaryText
				placementReady = res.Document.Ready
			}
		}

		source := tres.Source
		if source == "" {
			source = "llm"
		}
		out := onboardingChatResponse{
			Reply:            reply,
			Fields:           fields,
			Missing:          missing,
			Ready:            ready,
			FieldStatus:      status,
			Messages:         messages,
			Source:           source,
			PlacementSummary: placementSummary,
			PlacementReady:   placementReady,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func fieldsToAgentMap(f onboardingChatFields) map[string]any {
	m := map[string]any{}
	if f.TargetJobTitle != "" {
		m["target_job_title"] = f.TargetJobTitle
	}
	if f.ExperienceLevel != "" {
		m["experience_level"] = f.ExperienceLevel
	}
	if f.JobSearchStatus != "" {
		m["job_search_status"] = f.JobSearchStatus
	}
	if f.SalaryMin != nil {
		m["salary_min"] = *f.SalaryMin
	}
	if f.SalaryMax != nil {
		m["salary_max"] = *f.SalaryMax
	}
	if f.Currency != "" {
		m["currency"] = f.Currency
	}
	if len(f.PreferredRegions) > 0 {
		m["preferred_regions"] = f.PreferredRegions
	}
	if len(f.PreferredCountries) > 0 {
		m["preferred_countries"] = f.PreferredCountries
	}
	if len(f.PreferredLanguages) > 0 {
		m["preferred_languages"] = f.PreferredLanguages
	}
	if len(f.JobTypes) > 0 {
		m["job_types"] = f.JobTypes
	}
	if f.Country != "" {
		m["country"] = f.Country
	}
	if f.LinkedIn != "" {
		m["linkedin"] = f.LinkedIn
	}
	if f.ExtraInfo != "" {
		m["capabilities"] = f.ExtraInfo
		m["extra_info"] = f.ExtraInfo
	}
	return m
}

func agentMapToFields(m map[string]any) onboardingChatFields {
	var f onboardingChatFields
	if m == nil {
		return f
	}
	// Reuse JSON round-trip for type coercion.
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &f)
	// capabilities is the chat-agent name for CV text.
	if cap, ok := m["capabilities"].(string); ok && strings.TrimSpace(cap) != "" {
		if len(cap) >= len(strings.TrimSpace(f.ExtraInfo)) {
			f.ExtraInfo = cap
		}
	}
	return f
}

func mergeAgentMaps(base, overlay map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func historyToAgent(msgs []onboardingChatMessage) []chatagentclient.ChatMessage {
	out := make([]chatagentclient.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, chatagentclient.ChatMessage{Role: role, Content: m.Content})
	}
	return out
}
