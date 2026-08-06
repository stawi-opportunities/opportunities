package v1

import (
	"encoding/json"
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
// (candidate, context_key). Opportunity view uses one shared session for all
// jobs; the current listing is supplied each turn via documents/runtime.
// Durable state lives in chat-agent.
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
// Pass by pointer — contains sync.Once and must not be copied.
type MeChatAgentDeps struct {
	Client    *chatagentclient.Client
	Drafts    OnboardingDraftStore
	Placement *placement.Service
	Profiles  placement.ProfileStore
	// Sessions caches session ids (optional; nil uses ephemeral per request create).
	Sessions *sessionStore
	// ensureOnce registers product contexts once (best-effort).
	ensureOnce sync.Once
}

// MeChatAgentHandler is POST /me/chat — always the platform chat-agent path.
// There is no local MeChatHandler fallback: misconfiguration or agent errors
// surface as 503/502 so the SPA fails honestly.
// Opportunity side-chat passes opportunity{}.
func MeChatAgentHandler(deps *MeChatAgentDeps) http.HandlerFunc {
	if deps == nil || deps.Client == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			httpmw.ProblemJSON(w, http.StatusServiceUnavailable, "chat_agent_unavailable",
				"I can't process chat right now — the assistant is not configured on this environment. Please try again later or contact support.")
		}
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

		// Load prior draft for seed fields. Placement messages are intake-only;
		// opportunity threads never inherit or overwrite that transcript.
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

		// User-facing message stays clean. Job context is supplied via runtime +
		// the opportunity document so session transcripts never show chrome.
		userFacingMsg := stripViewingChrome(msg)

		runtime := map[string]any{}
		var listingDoc *chatagentclient.DocumentEvidence
		if mode == "opportunity" && in.Opportunity != nil {
			op := in.Opportunity
			runtime["opportunity_id"] = op.ID
			runtime["opportunity_slug"] = op.Slug
			runtime["opportunity_title"] = op.Title
			runtime["opportunity_entity"] = op.Entity
			runtime["opportunity_location"] = op.Location
			runtime["opportunity_kind"] = op.Kind
			if u := strings.TrimSpace(op.ApplyURL); u != "" {
				runtime["opportunity_apply_url"] = u
			}
			// Structured listing text so the model can extract title/company/location
			// and discuss fit without relying on session-seeded docs from an older job.
			listingDoc = &chatagentclient.DocumentEvidence{
				Name: "opportunity",
				Kind: "listing",
				Text: truncateRunes(formatOpportunityListingDoc(op), 14_000),
			}
			docs = append(docs, *listingDoc)
		}

		// One opportunity-view session per candidate (shared across all job pages).
		// Current listing is always supplied via runtime + documents on each turn.
		sessionKey := candidateID + "|" + contextKey

		// Placement: resume intake transcript. Opportunity: seed profile
		// fields/docs only — never onboarding chat history.
		var seedMsgs []chatagentclient.ChatMessage
		if mode != "opportunity" {
			seedMsgs = historyToAgent(filterPlacementMessages(stored.Messages))
		}

		sessionID := deps.Sessions.get(sessionKey)
		if sessionID == "" {
			sess, cerr := deps.Client.CreateSession(ctx, chatagentclient.CreateSessionRequest{
				SubjectID:        candidateID,
				ContextKey:       contextKey,
				InlineConfig:     &inline,
				SeedFields:       seed,
				Documents:        docs,
				SeedMessages:     seedMsgs,
				Runtime:          runtime,
				EvaluateEvidence: len(docs) > 0 || len(seed) > 0,
			})
			if cerr != nil {
				log.WithError(cerr).Error("me/chat: CreateSession failed")
				httpmw.ProblemJSON(w, http.StatusBadGateway, "chat_agent_session_failed",
					"I couldn't start the assistant session. Your message was not processed — please try again in a moment.")
				return
			}
			sessionID = sess.ID
			deps.Sessions.set(sessionKey, sessionID)
		}

		structured := map[string]any{}
		if li := normalizeLinkedIn(in.LinkedIn); li != "" {
			structured["linkedin"] = li
		}
		// Mirror current listing into structured so extractors can bind fields
		// when the seeker refers to "this job" / "this role".
		for k, v := range runtime {
			structured[k] = v
		}

		// Placement: only re-send CV when newly uploaded. Opportunity: always
		// attach the current listing so multi-job shared sessions stay on-page.
		var turnDocs []chatagentclient.DocumentEvidence
		if mode == "opportunity" {
			if listingDoc != nil {
				turnDocs = append(turnDocs, *listingDoc)
			}
			if cvText != "" {
				turnDocs = append(turnDocs, chatagentclient.DocumentEvidence{
					Name: "capabilities", Kind: "cv", Text: truncateRunes(cvText, 80_000),
				})
			}
		} else if cvText != "" {
			turnDocs = docs
		}

		tres, terr := deps.Client.Turn(ctx, chatagentclient.TurnRequest{
			SessionID:  sessionID,
			Message:    userFacingMsg,
			Structured: structured,
			Documents:  turnDocs,
		})
		if terr != nil {
			log.WithError(terr).Error("me/chat: Turn failed")
			// Drop bad session cache so next request recreates.
			deps.Sessions.set(sessionKey, "")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "chat_agent_turn_failed",
				"I couldn't process that message with the assistant just now. Nothing was saved from this turn — please try again.")
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
		// Never show canned guided copy when the model produced nothing.
		if strings.TrimSpace(reply) == "" {
			log.Warn("me/chat: Turn returned empty reply after compose")
			deps.Sessions.set(sessionKey, "")
			httpmw.ProblemJSON(w, http.StatusBadGateway, "chat_agent_empty_reply",
				"The assistant could not generate a reply for that turn. Nothing was saved — please try again.")
			return
		}

		// Map agent messages to SPA shape; always strip legacy viewing chrome.
		var messages []onboardingChatMessage
		if tres.Session != nil {
			for _, m := range tres.Session.Messages {
				messages = append(messages, onboardingChatMessage{Role: m.Role, Content: m.Content})
			}
		}
		if len(messages) == 0 {
			// Opportunity: use client job-thread history only (not placement draft).
			messages = appendChatTurn(sanitizeHistory(in.History), userFacingMsg, reply)
		}
		messages = sanitizeMessagesForClient(messages)

		// Placement/onboarding: persist full intake transcript.
		// Opportunity: update placement fields only — never replace intake messages.
		if deps.Drafts != nil && candidateID != "" {
			persistMsgs := messages
			persistReady := ready
			if mode == "opportunity" {
				persistMsgs = filterPlacementMessages(stored.Messages)
				// Don't advance wizard step solely from a job side-chat turn.
				persistReady = false
			}
			if err := persistChatSession(ctx, MeChatDeps{Drafts: deps.Drafts}, candidateID, stored, fields, persistMsgs, persistReady); err != nil {
				log.WithError(err).Warn("me/chat: draft persist failed")
			}
		}

		// Placement rebuild remains matching's responsibility.
		// Opportunity turns may refine fields but must not overwrite the
		// conversation-grounded intake digest with job-specific Q&A.
		var placementSummary string
		placementReady := ready
		if deps.Placement != nil && candidateID != "" {
			var turns []placement.ChatTurn
			if mode != "opportunity" {
				turns = make([]placement.ChatTurn, 0, len(messages))
				for _, m := range messages {
					turns = append(turns, placement.ChatTurn{Role: m.Role, Content: m.Content})
				}
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
		// Surface current listing card for the SPA widget (shared multi-job session).
		if mode == "opportunity" && in.Opportunity != nil {
			out.Card = opportunityCardFromContext(in.Opportunity)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// formatOpportunityListingDoc builds extractable plain text for the model from
// the page context the SPA sends (title, company, location, apply URL, body).
func formatOpportunityListingDoc(op *opportunityChatContext) string {
	if op == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("CURRENT LISTING (in view on the page)\n")
	if t := strings.TrimSpace(op.Title); t != "" {
		b.WriteString("Title: ")
		b.WriteString(t)
		b.WriteByte('\n')
	}
	if e := strings.TrimSpace(op.Entity); e != "" {
		b.WriteString("Company/entity: ")
		b.WriteString(e)
		b.WriteByte('\n')
	}
	if loc := strings.TrimSpace(op.Location); loc != "" {
		b.WriteString("Location: ")
		b.WriteString(loc)
		b.WriteByte('\n')
	}
	if k := strings.TrimSpace(op.Kind); k != "" {
		b.WriteString("Kind: ")
		b.WriteString(k)
		b.WriteByte('\n')
	}
	if id := strings.TrimSpace(op.ID); id != "" {
		b.WriteString("ID: ")
		b.WriteString(id)
		b.WriteByte('\n')
	}
	if s := strings.TrimSpace(op.Slug); s != "" {
		b.WriteString("Slug: ")
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if u := strings.TrimSpace(op.ApplyURL); u != "" {
		b.WriteString("Apply URL: ")
		b.WriteString(u)
		b.WriteByte('\n')
	}
	if d := strings.TrimSpace(op.Description); d != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func opportunityCardFromContext(op *opportunityChatContext) *opportunityChatCard {
	if op == nil || strings.TrimSpace(op.Title) == "" {
		return nil
	}
	card := &opportunityChatCard{
		Title:         strings.TrimSpace(op.Title),
		Subtitle:      strings.TrimSpace(strings.Join(nonEmptyJoin(op.Entity, op.Location), " · ")),
		Href:          listingPath(op.Kind, op.Slug),
		ApplyURL:      strings.TrimSpace(op.ApplyURL),
		OpportunityID: strings.TrimSpace(op.ID),
		Slug:          strings.TrimSpace(op.Slug),
	}
	return card
}

func nonEmptyJoin(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func listingPath(kind, slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "/"
	}
	prefix := "jobs"
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "scholarship", "scholarships":
		prefix = "scholarships"
	case "tender", "tenders":
		prefix = "tenders"
	case "deal", "deals":
		prefix = "deals"
	case "funding":
		prefix = "funding"
	}
	return "/" + prefix + "/" + slug + "/"
}

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

// stripViewingChrome removes the legacy "[Viewing opportunity: …]" prefix that
// older clients/servers injected into user messages for the model.
func stripViewingChrome(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[Viewing opportunity:") {
		return s
	}
	if i := strings.Index(s, "]"); i >= 0 && i+1 < len(s) {
		return strings.TrimSpace(s[i+1:])
	}
	return ""
}

// isOpportunityThreadNoise identifies turns that belong to job side-chat and
// must never appear in placement/onboarding intake history.
func isOpportunityThreadNoise(m onboardingChatMessage) bool {
	c := strings.TrimSpace(m.Content)
	if c == "" {
		return true
	}
	if strings.HasPrefix(c, "[Viewing opportunity:") {
		return true
	}
	role := strings.ToLower(strings.TrimSpace(m.Role))
	if role == "assistant" && strings.HasPrefix(c, "You're viewing ") {
		return true
	}
	return false
}

// filterPlacementMessages drops job-side-chat chrome so intake transcripts stay
// focused on matching-profile collection.
func filterPlacementMessages(msgs []onboardingChatMessage) []onboardingChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]onboardingChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if isOpportunityThreadNoise(m) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := m.Content
		if role == "user" {
			content = stripViewingChrome(content)
			if strings.TrimSpace(content) == "" {
				continue
			}
		}
		out = append(out, onboardingChatMessage{Role: role, Content: content})
	}
	return out
}

// sanitizeMessagesForClient normalizes roles and strips viewing chrome so SPA
// bubbles never render model-only prefixes.
func sanitizeMessagesForClient(msgs []onboardingChatMessage) []onboardingChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]onboardingChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if role == "user" {
			content = stripViewingChrome(content)
		}
		if content == "" {
			continue
		}
		out = append(out, onboardingChatMessage{
			Role:    role,
			Content: truncateRunes(content, 12_000),
		})
	}
	return clampChatMessages(out, 80)
}
