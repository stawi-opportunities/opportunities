package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
	"github.com/stawi-opportunities/opportunities/pkg/placement"
)

// MeChatLLM is the minimal surface the preference chat needs from the
// extraction package. *extraction.Extractor satisfies it via Complete().
// Nil disables AI and falls back to a deterministic heuristic parser.
type MeChatLLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// MeChatDeps wires the shared preference / intake chat turn handler.
type MeChatDeps struct {
	LLM MeChatLLM // optional
	// Drafts optionally persists the conversation + extracted fields so
	// the seeker can resume any time (including after onboard).
	Drafts OnboardingDraftStore
	// Placement rebuilds qualifications+preferences summary + vector index.
	// Also used to rehydrate CV text from prior placement when the draft
	// lost capabilities so a past upload is never demanded again.
	Placement *placement.Service
	// Profiles optional (falls back to Placement.Profiles): file-id on
	// candidate_profiles proves a CV was stored even when text is thin.
	Profiles placement.ProfileStore
	Now      func() time.Time
}

func (d MeChatDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// onboardingChatMessage is one turn in the conversation.
type onboardingChatMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// onboardingChatFields is the structured profile we fill during chat.
// Mirrors OnboardingDraftFields / onboardPayload (without plan/terms).
//
// Required for plan unlock (assessFieldStatus):
//
//	target role, CV (capabilities), job types to notify on,
//	salary expectation, preferred opportunity countries, experience level.
//
// LinkedIn is optional if the seeker shares it; it is not required.
type onboardingChatFields struct {
	TargetJobTitle     string   `json:"target_job_title,omitempty"`
	ExperienceLevel    string   `json:"experience_level,omitempty"`
	JobSearchStatus    string   `json:"job_search_status,omitempty"`
	SalaryMin          *float64 `json:"salary_min,omitempty"`
	SalaryMax          *float64 `json:"salary_max,omitempty"`
	Currency           string   `json:"currency,omitempty"`
	PreferredRegions   []string `json:"preferred_regions,omitempty"`
	PreferredCountries []string `json:"preferred_countries,omitempty"` // opportunity source countries
	PreferredTimezones []string `json:"preferred_timezones,omitempty"`
	PreferredLanguages []string `json:"preferred_languages,omitempty"`
	JobTypes           []string `json:"job_types,omitempty"`  // kinds of jobs to notify about
	Country            string   `json:"country,omitempty"`    // home / base country
	LinkedIn           string   `json:"linkedin,omitempty"`   // profile URL or handle
	ExtraInfo          string   `json:"extra_info,omitempty"` // CV paste / skills summary
}

// onboardingChatRequest is the body of POST /me/chat.
// LinkedIn / CV may be supplied as structured fields (composer actions)
// in addition to free-text so the server can always run full inference.
type onboardingChatRequest struct {
	Message    string                  `json:"message"`
	History    []onboardingChatMessage `json:"history,omitempty"`
	Draft      onboardingChatFields    `json:"draft,omitempty"`
	LinkedIn   string                  `json:"linkedin,omitempty"`    // URL or handle from composer
	CVText     string                  `json:"cv_text,omitempty"`     // extracted CV plain text
	CVFilename string                  `json:"cv_filename,omitempty"` // original filename for audit
}

// fieldStatus is per-required-field readiness for the client / debugging.
type fieldStatus struct {
	OK     bool   `json:"ok"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"` // empty when ok
}

// onboardingChatResponse is returned after each turn.
// Ready is ALWAYS computed server-side from validated fields — never trusted
// from the LLM. Missing is the ordered list of required keys still incomplete.
type onboardingChatResponse struct {
	Reply       string                 `json:"reply"`
	Fields      onboardingChatFields   `json:"fields"`
	Missing     []string               `json:"missing"`
	Ready       bool                   `json:"ready"`
	FieldStatus map[string]fieldStatus `json:"field_status"`
	// Messages is the full persisted transcript after this turn (user+assistant).
	Messages []onboardingChatMessage `json:"messages,omitempty"`
	// Source: "llm" | "heuristic" | "llm+heuristic" — how fields were filled.
	Source string `json:"source"`
	// PlacementSummary is the combined qualifications+preferences document.
	PlacementSummary string `json:"placement_summary,omitempty"`
	// PlacementReady mirrors summary completeness for UI.
	PlacementReady bool `json:"placement_ready,omitempty"`
}

// requiredChatFieldOrder is the priority order for follow-up questions.
// Only these gate ready → plan selection. Soft fields (search status,
// languages, regions) get safe defaults when possible.
var requiredChatFieldOrder = []string{
	"target_job_title",
	"capabilities", // CV paste / upload (extra_info) — LinkedIn is optional
	"job_types",
	"salary_expectation",
	"preferred_countries",
	"experience_level",
}

var allowedJobTypes = map[string]string{
	"full-time": "Full-time", "full time": "Full-time", "fulltime": "Full-time",
	"part-time": "Part-time", "part time": "Part-time", "parttime": "Part-time",
	"contract": "Contract", "contractor": "Contract",
	"freelance": "Freelance", "freelancer": "Freelance",
	"internship": "Internship", "intern": "Internship",
	// Work-mode tokens are mapped to Full-time so matching still has a type.
	"remote": "Full-time", "hybrid": "Full-time", "onsite": "Full-time", "on-site": "Full-time",
}

var allowedRegions = map[string]string{
	"anywhere": "Anywhere", "worldwide": "Anywhere", "global": "Anywhere",
	"africa": "Africa", "europe": "Europe",
	"north america": "North America", "northamerica": "North America",
	"south america": "South America", "southamerica": "South America",
	"asia": "Asia", "oceania": "Oceania", "middle east": "Middle East",
}

// MeChatHandler serves POST /me/chat — one conversational turn shared by
// onboarding, dashboard refine, and opportunity side-chat embeds.
// The server:
//  1. Loads any persisted transcript + draft (source of truth for resume)
//  2. Merges free-text (full conversation corpus) into structured fields
//  3. Sanitizes / validates every field against the onboard schema
//  4. Decides ready/missing solely from those validated fields
//  5. Persists conversation + fields so they remain always available
//  6. Returns a focused follow-up when anything required is still missing
func MeChatHandler(deps MeChatDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		candidateID := httpmw.CandidateFromContext(ctx)

		body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"body_read_failed", "could not read request body")
			return
		}
		var in onboardingChatRequest
		if err := json.Unmarshal(body, &in); err != nil {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"invalid_json", "request body is not valid JSON")
			return
		}
		msg := strings.TrimSpace(in.Message)
		cvText := strings.TrimSpace(in.CVText)
		if len([]rune(cvText)) > 80_000 {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"cv_too_long", "cv_text must be under 80000 characters")
			return
		}
		// Allow structured LinkedIn/CV-only turns (composer actions) with no free text.
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
			default:
				httpmw.ProblemJSON(w, http.StatusBadRequest,
					"empty_message", "message, linkedin, or cv_text is required")
				return
			}
		}
		if len([]rune(msg)) > 50_000 {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"message_too_long", "message must be under 50000 characters")
			return
		}

		// Load server session first so resume works even if the client
		// sends a partial history/draft (e.g. after refresh).
		stored, storedErr := loadOnboardingEnvelope(ctx, deps.Drafts, candidateID)
		if storedErr != nil {
			log.WithError(storedErr).WithField("candidate_id", candidateID).
				Warn("me/chat: could not load prior session; continuing without")
		}
		history := mergeChatHistory(stored.Messages, in.History)
		priorFields := fieldsFromEnvelope(stored)
		// Rehydrate CV / capabilities from placement or file-id so a past
		// upload survives refresh and never forces the seeker back to step 1.
		priorFields = hydrateCapabilities(ctx, deps, candidateID, priorFields)
		merged := mergeChatFields(sanitizeFields(priorFields), sanitizeFields(in.Draft))
		merged = hydrateCapabilities(ctx, deps, candidateID, merged)

		// Structured composer inputs — apply before inference.
		if li := normalizeLinkedIn(in.LinkedIn); li != "" {
			merged.LinkedIn = li
		}
		if cvText != "" {
			// Prefer longer CV text for capabilities + skill extraction.
			if len(cvText) >= len(strings.TrimSpace(merged.ExtraInfo)) {
				merged.ExtraInfo = truncateRunes(cvText, 8000)
			} else {
				merged.ExtraInfo = appendExtra(merged.ExtraInfo, cvText)
			}
		}

		// Full seeker transcript (prior user turns + this message + CV)
		// so extraction can re-read earlier answers when the latest turn
		// only fills one gap.
		userCorpus := collectUserCorpus(history, msg)
		if cvText != "" {
			userCorpus = strings.TrimSpace(userCorpus + "\n\n" + truncateRunes(cvText, 12000))
		}
		// Only store free-text as capabilities when it actually looks like a CV.
		// Short preference answers must not accumulate into a fake CV.
		if msg != "" && cvText == "" && looksLikeCV(msg) {
			merged.ExtraInfo = appendExtra(merged.ExtraInfo, msg)
		}

		source := "heuristic"
		var llmReply string

		if deps.LLM != nil {
			extracted, reply, llmErr := chatExtractWithLLM(ctx, deps.LLM, history, msg, merged, userCorpus)
			if llmErr != nil {
				log.WithError(llmErr).Warn("me/chat: LLM failed; using heuristic")
				merged = mergeChatFields(merged, heuristicExtract(userCorpus))
			} else {
				source = "llm"
				merged = mergeChatFields(merged, extracted)
				llmReply = strings.TrimSpace(reply)
				// Fill any gaps the model left with the full-corpus heuristic.
				before := missingChatFields(merged)
				merged = mergeChatFields(merged, heuristicExtract(userCorpus))
				if len(missingChatFields(merged)) < len(before) {
					source = "llm+heuristic"
				}
			}
		} else {
			merged = mergeChatFields(merged, heuristicExtract(userCorpus))
		}

		// Re-apply structured inputs after extract so LLM cannot wipe them.
		if li := normalizeLinkedIn(in.LinkedIn); li != "" {
			merged.LinkedIn = li
		}
		if li := normalizeLinkedIn(merged.LinkedIn); li != "" {
			merged.LinkedIn = li
		}
		if cvText != "" && !looksLikeCV(merged.ExtraInfo) {
			merged.ExtraInfo = truncateRunes(cvText, 8000)
		}

		// Final sanitize after all merges — drop junk, normalize enums.
		merged = sanitizeFields(merged)
		// Apply only *safe* defaults that do not invent identity (role/country).
		merged = applySafeDefaults(merged)

		status := assessFieldStatus(merged)
		missing := missingFromStatus(status)
		ready := len(missing) == 0

		reply := composeReply(llmReply, merged, missing, ready)

		// Append this turn and persist so the conversation is always available.
		nextMessages := appendChatTurn(history, msg, reply)
		if deps.Drafts != nil && candidateID != "" {
			if err := persistChatSession(ctx, deps, candidateID, stored, merged, nextMessages, ready); err != nil {
				log.WithError(err).WithField("candidate_id", candidateID).
					Warn("me/chat: session persist failed (turn still returned)")
			}
		}

		// Rebuild placement profile (qualifications + preferences) and refresh
		// the match vector when we have enough signal.
		var placementSummary string
		placementReady := ready
		if deps.Placement != nil && candidateID != "" {
			// Conversation-grounded persona: fold chat turns into matching digest.
			turns := make([]placement.ChatTurn, 0, len(nextMessages))
			for _, m := range nextMessages {
				turns = append(turns, placement.ChatTurn{Role: m.Role, Content: m.Content})
			}
			res, pErr := deps.Placement.Rebuild(ctx, placement.RebuildInput{
				CandidateID: candidateID,
				Fields:      toPlacementFields(merged),
				ChatTurns:   turns,
			})
			if pErr != nil {
				log.WithError(pErr).WithField("candidate_id", candidateID).
					Warn("me/chat: placement rebuild failed")
			} else if res != nil {
				placementSummary = res.Document.SummaryText
				placementReady = res.Document.Ready
				if res.Embedded {
					log.WithField("candidate_id", candidateID).
						WithField("version", res.Version).
						Info("me/chat: placement profile embedded for matching")
				}
			}
		}

		out := onboardingChatResponse{
			Reply:            reply,
			Fields:           merged,
			Missing:          missing,
			Ready:            ready,
			FieldStatus:      status,
			Messages:         nextMessages,
			Source:           source,
			PlacementSummary: placementSummary,
			PlacementReady:   placementReady,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func toPlacementFields(f onboardingChatFields) placement.Fields {
	return placement.Fields{
		TargetJobTitle:     f.TargetJobTitle,
		ExperienceLevel:    f.ExperienceLevel,
		JobSearchStatus:    f.JobSearchStatus,
		SalaryMin:          f.SalaryMin,
		SalaryMax:          f.SalaryMax,
		Currency:           f.Currency,
		PreferredRegions:   f.PreferredRegions,
		PreferredCountries: f.PreferredCountries,
		PreferredTimezones: f.PreferredTimezones,
		PreferredLanguages: f.PreferredLanguages,
		JobTypes:           f.JobTypes,
		Country:            f.Country,
		LinkedIn:           f.LinkedIn,
		ExtraInfo:          f.ExtraInfo,
	}
}

func fieldsFromEnvelope(env onboardingEnvelope) onboardingChatFields {
	var f onboardingChatFields
	if len(env.Fields) == 0 || string(env.Fields) == "null" {
		return f
	}
	_ = json.Unmarshal(env.Fields, &f)
	return f
}

// mergeChatHistory prefers the longer transcript so server resume and
// client in-flight turns both contribute. Dedupes consecutive identical turns.
func mergeChatHistory(server, client []onboardingChatMessage) []onboardingChatMessage {
	s := sanitizeHistory(server)
	c := sanitizeHistory(client)
	if len(c) > len(s) {
		return c
	}
	if len(s) > 0 {
		return s
	}
	return c
}

func sanitizeHistory(in []onboardingChatMessage) []onboardingChatMessage {
	var out []onboardingChatMessage
	for _, m := range in {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
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

func appendChatTurn(history []onboardingChatMessage, userMsg, assistantReply string) []onboardingChatMessage {
	out := sanitizeHistory(history)
	out = append(out, onboardingChatMessage{
		Role:    "user",
		Content: truncateRunes(strings.TrimSpace(userMsg), 12_000),
	})
	if ar := strings.TrimSpace(assistantReply); ar != "" {
		out = append(out, onboardingChatMessage{
			Role:    "assistant",
			Content: truncateRunes(ar, 4_000),
		})
	}
	return clampChatMessages(out, 80)
}

func clampChatMessages(msgs []onboardingChatMessage, max int) []onboardingChatMessage {
	if max <= 0 || len(msgs) <= max {
		return msgs
	}
	return msgs[len(msgs)-max:]
}

func persistChatSession(
	ctx context.Context,
	deps MeChatDeps,
	candidateID string,
	prior onboardingEnvelope,
	fields onboardingChatFields,
	messages []onboardingChatMessage,
	ready bool,
) error {
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	// Step is monotonic: once the seeker reaches plan selection (2) or
	// payment (3), never push them back to chat-only (1) on a later turn.
	step := prior.Step
	if step < 1 {
		step = 1
	}
	if ready && step < 2 {
		step = 2
	}
	// Preserve plan and other wizard-only keys that aren't chat fields.
	mergedFields := mergeRawFields(prior.Fields, fieldsJSON)
	now := deps.now()
	env := onboardingEnvelope{
		Step:      step,
		Fields:    mergedFields,
		Messages:  messages,
		UpdatedAt: &now,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return deps.Drafts.SetOnboardingDraft(ctx, candidateID, raw)
}

// mergeRawFields overlays chat field keys onto the prior wizard JSON so
// plan / wants_ats_report / etc. survive chat turns.
func mergeRawFields(prior, overlay json.RawMessage) json.RawMessage {
	base := map[string]json.RawMessage{}
	if len(prior) > 0 && string(prior) != "null" {
		_ = json.Unmarshal(prior, &base)
	}
	over := map[string]json.RawMessage{}
	if len(overlay) > 0 {
		_ = json.Unmarshal(overlay, &over)
	}
	for k, v := range over {
		// Skip empty strings / empty arrays so we don't wipe prior values
		// with zero-values from partial marshal.
		if string(v) == `""` || string(v) == `null` || string(v) == `[]` {
			continue
		}
		base[k] = v
	}
	out, err := json.Marshal(base)
	if err != nil {
		return overlay
	}
	return out
}

// collectUserCorpus concatenates all user-authored text in order so the
// extractor can re-read earlier turns (e.g. title on turn 1, country on turn 2).
func collectUserCorpus(history []onboardingChatMessage, latest string) string {
	var b strings.Builder
	for _, m := range history {
		if strings.EqualFold(strings.TrimSpace(m.Role), "user") {
			c := strings.TrimSpace(m.Content)
			if c == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(truncateRunes(c, 8000))
		}
		if b.Len() > 40_000 {
			break
		}
	}
	if latest = strings.TrimSpace(latest); latest != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(truncateRunes(latest, 12000))
	}
	return b.String()
}

func appendExtra(prior, msg string) string {
	msg = strings.TrimSpace(msg)
	prior = strings.TrimSpace(prior)
	if prior == "" {
		return truncateRunes(msg, 4000)
	}
	if msg == "" {
		return truncateRunes(prior, 4000)
	}
	// Avoid duplicating the same paste.
	if strings.Contains(prior, msg) {
		return truncateRunes(prior, 4000)
	}
	return truncateRunes(prior+"\n\n"+msg, 4000)
}

func placementGuideBlock(missing []string) string {
	if len(missing) == 0 {
		return "## Collection status\nAll required placement signals are present. Confirm and invite plan selection."
	}
	var b strings.Builder
	b.WriteString("## Collection status — guide the seeker\n")
	b.WriteString("Next focus: " + missing[0] + "\n")
	for _, k := range missing {
		g := placement.FieldGuide[k]
		if g.Label == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s): %s Ask: %s\n", g.Label, k, g.Why, g.Ask)
	}
	return b.String()
}

func chatExtractWithLLM(
	ctx context.Context,
	llm MeChatLLM,
	history []onboardingChatMessage,
	userMsg string,
	prior onboardingChatFields,
	userCorpus string,
) (onboardingChatFields, string, error) {
	priorJSON, _ := json.Marshal(prior)
	missingPrior := missingChatFields(prior)
	missingJSON, _ := json.Marshal(missingPrior)

	var hist strings.Builder
	// Keep last ~12 turns (user+assistant interleaved).
	start := 0
	if len(history) > 24 {
		start = len(history) - 24
	}
	for _, m := range history[start:] {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if hist.Len() > 14_000 {
			break
		}
		fmt.Fprintf(&hist, "%s: %s\n", role, truncateRunes(m.Content, 3000))
	}

	// Agent harness: make the model explicitly guide collection of the
	// placement profile (qualifications + preferences for matching).
	guide := placementGuideBlock(missingPrior)
	prompt := fmt.Sprintf(`You are Stawi's placement intake agent for opportunity seekers (jobs first).
You build a PLACEMENT PROFILE that combines:
  (A) Qualifications — CV / work history (what they can do)
  (B) Preferences — role, level, job types, salary, markets (what they want)
This profile is vector-indexed for matching. Incomplete profiles produce poor matches.

## Your job each turn
1. Extract structured fields from the full conversation + CV text.
2. Guide the seeker toward the next missing REQUIRED signal (see missing_before_turn).
3. Briefly acknowledge what you already understood.
4. Explain WHY the next ask improves placement when helpful (one short clause).

## Required before plan selection (priority order)
1. target_job_title — concrete role (drives semantic match)
2. capabilities — pasted/uploaded CV / substantial work history (LinkedIn optional, not required)
3. job_types — Full-time, Part-time, Contract, Freelance, Internship
4. salary_expectation — salary_min and/or salary_max (+ currency)
5. preferred_countries — markets to source opportunities from (ISO when possible)
6. experience_level — entry|junior|mid|senior|lead|executive

%s

## Output
Return ONLY a single JSON object (no markdown fences, no prose outside JSON):
{
  "fields": {
    "target_job_title": string,
    "experience_level": "entry"|"junior"|"mid"|"senior"|"lead"|"executive"|"" ,
    "job_search_status": "actively_looking"|"open_to_offers"|"casually_browsing"|"" ,
    "salary_min": number|null,
    "salary_max": number|null,
    "currency": string,
    "preferred_regions": string[],
    "preferred_countries": string[],
    "preferred_timezones": string[],
    "preferred_languages": string[],
    "job_types": string[],
    "country": string,
    "linkedin": string,
    "extra_info": string
  },
  "reply": string
}

## Rules (critical)
1. ONLY fill a field when the conversation (including past user turns and CV text) clearly supports it.
2. NEVER invent a job title, country, salary, or LinkedIn. If unsure, leave empty.
3. "actively looking for full-time roles" does NOT set target_job_title.
4. Prefer ISO country codes: Kenya=KE, Uganda=UG, Nigeria=NG, Ghana=GH, South Africa=ZA, US=US, UK=GB.
5. preferred_countries = opportunity source markets. Home country alone → country AND preferred_countries.
6. job_types = employment kinds (map bare remote → Full-time).
7. CV paste/upload → full useful text in extra_info; extract title/level/skills from it.
8. LinkedIn is optional; never block readiness on it.
9. reply: 1–3 short sentences. Acknowledge known profile. Ask ONLY for the single highest-priority missing item. When complete, invite plan selection. Never claim ready if missing_before_turn is non-empty.
10. Merge with existing_draft — do not wipe prior fields.

## existing_draft
%s

## missing_before_turn
%s

## conversation
%s

## full_user_corpus
%s

## latest_user_message
%s
`, guide, string(priorJSON), string(missingJSON), hist.String(),
		truncateRunes(userCorpus, 12000), truncateRunes(userMsg, 8000))

	raw, err := llm.Complete(ctx, prompt)
	if err != nil {
		return onboardingChatFields{}, "", err
	}
	raw = extractJSONObject(raw)

	var parsed struct {
		Fields onboardingChatFields `json:"fields"`
		Reply  string               `json:"reply"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return onboardingChatFields{}, "", fmt.Errorf("decode llm json: %w", err)
	}
	return parsed.Fields, parsed.Reply, nil
}

func mergeChatFields(base, overlay onboardingChatFields) onboardingChatFields {
	out := base
	if v := strings.TrimSpace(overlay.TargetJobTitle); v != "" && !junkTitle(v) {
		out.TargetJobTitle = v
	}
	if v := normalizeExperience(overlay.ExperienceLevel); v != "" {
		out.ExperienceLevel = v
	}
	if v := normalizeSearchStatus(overlay.JobSearchStatus); v != "" {
		out.JobSearchStatus = v
	}
	if overlay.SalaryMin != nil && *overlay.SalaryMin >= 0 {
		out.SalaryMin = overlay.SalaryMin
	}
	if overlay.SalaryMax != nil && *overlay.SalaryMax >= 0 {
		out.SalaryMax = overlay.SalaryMax
	}
	if v := strings.TrimSpace(overlay.Currency); len(v) == 3 {
		out.Currency = strings.ToUpper(v)
	}
	if len(overlay.PreferredRegions) > 0 {
		out.PreferredRegions = normalizeRegions(overlay.PreferredRegions)
	}
	if len(overlay.PreferredCountries) > 0 {
		out.PreferredCountries = normalizeCountryList(overlay.PreferredCountries)
	}
	if len(overlay.PreferredTimezones) > 0 {
		out.PreferredTimezones = uniqueNonEmpty(overlay.PreferredTimezones)
	}
	if len(overlay.PreferredLanguages) > 0 {
		out.PreferredLanguages = normalizeLanguages(overlay.PreferredLanguages)
	}
	if len(overlay.JobTypes) > 0 {
		out.JobTypes = normalizeJobTypes(overlay.JobTypes)
	}
	if v := normalizeCountry(overlay.Country); v != "" {
		out.Country = v
	}
	if v := normalizeLinkedIn(overlay.LinkedIn); v != "" {
		out.LinkedIn = v
	}
	if v := strings.TrimSpace(overlay.ExtraInfo); v != "" {
		// Prefer longer CV paste when merging.
		if len(v) >= len(strings.TrimSpace(out.ExtraInfo)) {
			out.ExtraInfo = v
		}
	}
	return out
}

// sanitizeFields normalizes enums and drops invalid values so readiness
// assessment is trustworthy regardless of LLM/heuristic noise.
func sanitizeFields(f onboardingChatFields) onboardingChatFields {
	out := f
	title := strings.TrimSpace(out.TargetJobTitle)
	if junkTitle(title) || !plausibleTitle(title) {
		out.TargetJobTitle = ""
	} else {
		out.TargetJobTitle = collapseSpace(title)
	}
	out.ExperienceLevel = normalizeExperience(out.ExperienceLevel)
	out.JobSearchStatus = normalizeSearchStatus(out.JobSearchStatus)
	out.Country = normalizeCountry(out.Country)
	// Reject non-country tokens like "remote" or "africa" as country.
	if out.Country != "" && !looksLikeCountry(out.Country) {
		out.Country = ""
	}
	out.PreferredCountries = normalizeCountryList(out.PreferredCountries)
	out.PreferredRegions = normalizeRegions(out.PreferredRegions)
	out.PreferredLanguages = normalizeLanguages(out.PreferredLanguages)
	out.JobTypes = normalizeJobTypes(out.JobTypes)
	out.LinkedIn = normalizeLinkedIn(out.LinkedIn)
	if out.Currency != "" && len(out.Currency) != 3 {
		out.Currency = ""
	}
	if out.Currency != "" {
		out.Currency = strings.ToUpper(out.Currency)
	}
	// Drop nonsense salaries.
	if out.SalaryMin != nil && *out.SalaryMin <= 0 {
		out.SalaryMin = nil
	}
	if out.SalaryMax != nil && *out.SalaryMax <= 0 {
		out.SalaryMax = nil
	}
	return out
}

// applySafeDefaults fills ONLY fields that can be inferred without inventing
// the seeker's identity (title/linkedin/salary). Identity stays empty until stated.
func applySafeDefaults(f onboardingChatFields) onboardingChatFields {
	out := f
	if out.JobSearchStatus == "" && out.TargetJobTitle != "" {
		out.JobSearchStatus = "actively_looking"
	}
	// Long free-text / CV paste → assume English posts unless said otherwise.
	if len(out.PreferredLanguages) == 0 && (out.TargetJobTitle != "" || len(out.ExtraInfo) > 120) {
		out.PreferredLanguages = []string{"English"}
	}
	if len(out.PreferredRegions) == 0 && out.Country != "" {
		out.PreferredRegions = regionForCountry(out.Country)
	}
	// Opportunity countries: seed from home country only when user named one.
	if len(out.PreferredCountries) == 0 && out.Country != "" {
		out.PreferredCountries = []string{out.Country}
	}
	if len(out.JobTypes) == 0 && out.TargetJobTitle != "" {
		out.JobTypes = []string{"Full-time"}
	}
	if out.Currency == "" && (out.SalaryMin != nil || out.SalaryMax != nil) {
		out.Currency = "USD"
	}
	return out
}

func assessFieldStatus(f onboardingChatFields) map[string]fieldStatus {
	st := map[string]fieldStatus{}

	title := strings.TrimSpace(f.TargetJobTitle)
	if title != "" && !junkTitle(title) && plausibleTitle(title) {
		st["target_job_title"] = fieldStatus{OK: true, Value: title}
	} else {
		st["target_job_title"] = fieldStatus{OK: false, Reason: "need a concrete role title"}
	}

	// Capabilities: substantial CV / work history (LinkedIn is optional, not required).
	if hasCapabilities(f) {
		st["capabilities"] = fieldStatus{OK: true, Value: "CV / work history provided"}
	} else {
		st["capabilities"] = fieldStatus{
			OK:     false,
			Reason: "need a pasted or uploaded CV so we can assess capabilities",
		}
	}

	if types := normalizeJobTypes(f.JobTypes); len(types) > 0 {
		st["job_types"] = fieldStatus{OK: true, Value: strings.Join(types, ", ")}
	} else {
		st["job_types"] = fieldStatus{OK: false, Reason: "need kinds of jobs to notify about"}
	}

	if hasSalaryExpectation(f) {
		st["salary_expectation"] = fieldStatus{OK: true, Value: formatSalary(f)}
	} else {
		st["salary_expectation"] = fieldStatus{
			OK:     false,
			Reason: "need salary expectations (min and/or max)",
		}
	}

	if countries := normalizeCountryList(f.PreferredCountries); len(countries) > 0 {
		st["preferred_countries"] = fieldStatus{OK: true, Value: strings.Join(countries, ", ")}
	} else {
		st["preferred_countries"] = fieldStatus{
			OK:     false,
			Reason: "need countries to source opportunities from",
		}
	}

	if v := normalizeExperience(f.ExperienceLevel); v != "" {
		st["experience_level"] = fieldStatus{OK: true, Value: v}
	} else {
		st["experience_level"] = fieldStatus{OK: false, Reason: "need entry/junior/mid/senior/lead/executive"}
	}

	return st
}

func hasCapabilities(f onboardingChatFields) bool {
	s := strings.TrimSpace(f.ExtraInfo)
	if looksLikeCV(s) {
		return true
	}
	// Already-uploaded resume pointer (file-backed) written by hydrateCapabilities.
	low := strings.ToLower(s)
	if len(s) >= 40 && (strings.Contains(low, "cv on file") ||
		strings.Contains(low, "uploaded cv") ||
		strings.Contains(low, "resume document stored")) {
		return true
	}
	// Substantial free-form work history without standard section headers.
	if len(s) >= 400 {
		return true
	}
	return false
}

// hydrateCapabilities fills ExtraInfo from placement qualifications or a
// stored CV file-id so readiness is not lost after refresh / plan stage.
func hydrateCapabilities(
	ctx context.Context,
	deps MeChatDeps,
	candidateID string,
	f onboardingChatFields,
) onboardingChatFields {
	if hasCapabilities(f) || candidateID == "" {
		return f
	}
	// Prefer prior placement qualifications (full extracted CV text).
	if deps.Placement != nil && deps.Placement.Store != nil {
		if prior, err := deps.Placement.Store.Get(ctx, candidateID); err == nil && prior != nil {
			q := strings.TrimSpace(strings.TrimPrefix(prior.QualificationsText, "## Qualifications"))
			q = strings.TrimSpace(q)
			if q != "" && q != "(CV not yet provided)" {
				if looksLikeCV(q) || len(q) >= 120 {
					f.ExtraInfo = q
					return f
				}
			}
		}
	}
	// File-id on candidate_profiles: seeker already uploaded a binary.
	profiles := deps.Profiles
	if profiles == nil && deps.Placement != nil {
		profiles = deps.Placement.Profiles
	}
	if profiles != nil {
		if ref, err := profiles.GetCVFileRef(ctx, candidateID); err == nil {
			if strings.TrimSpace(ref.FileID) != "" || strings.TrimSpace(ref.ContentURI) != "" {
				if strings.TrimSpace(f.ExtraInfo) == "" {
					f.ExtraInfo = "Uploaded CV on file. Resume document stored for matching (experience, education, skills)."
				}
			}
		}
	}
	return f
}

func looksLikeCV(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 80 {
		return false
	}
	low := strings.ToLower(s)
	markers := 0
	for _, m := range []string{
		"experience", "education", "skills", "employment", "curriculum",
		"resume", "cv", "worked at", "responsibilities", "bachelor", "master",
		"mba", "bsc", "msc", "university", "years of experience",
	} {
		if strings.Contains(low, m) {
			markers++
		}
	}
	// Mini CVs with clear section structure.
	if markers >= 2 {
		return true
	}
	// Single strong marker + decent length (not mere chat accumulation).
	if markers >= 1 && len(s) >= 200 {
		return true
	}
	// Very long free-form work history with at least one career signal.
	if markers >= 1 && len(s) >= 600 {
		return true
	}
	return false
}

func hasSalaryExpectation(f onboardingChatFields) bool {
	if f.SalaryMin != nil && *f.SalaryMin > 0 {
		return true
	}
	if f.SalaryMax != nil && *f.SalaryMax > 0 {
		return true
	}
	return false
}

func formatSalary(f onboardingChatFields) string {
	cur := f.Currency
	if cur == "" {
		cur = "USD"
	}
	switch {
	case f.SalaryMin != nil && f.SalaryMax != nil:
		return fmt.Sprintf("%s %.0f–%.0f", cur, *f.SalaryMin, *f.SalaryMax)
	case f.SalaryMin != nil:
		return fmt.Sprintf("%s %.0f+", cur, *f.SalaryMin)
	case f.SalaryMax != nil:
		return fmt.Sprintf("%s up to %.0f", cur, *f.SalaryMax)
	default:
		return ""
	}
}

func missingFromStatus(st map[string]fieldStatus) []string {
	var miss []string
	for _, key := range requiredChatFieldOrder {
		if s, ok := st[key]; !ok || !s.OK {
			miss = append(miss, key)
		}
	}
	return miss
}

func missingChatFields(f onboardingChatFields) []string {
	return missingFromStatus(assessFieldStatus(f))
}

// composeReply prefers the model's conversational reply when it is useful,
// but never claims readiness if the server still has missing fields.
// When anything required is missing we always ask for the next gap.
func composeReply(llmReply string, f onboardingChatFields, missing []string, ready bool) string {
	llmReply = strings.TrimSpace(llmReply)
	if ready {
		if llmReply != "" && !looksLikeAskingForMore(llmReply) {
			return llmReply
		}
		title := f.TargetJobTitle
		if title == "" {
			title = "your target role"
		}
		where := strings.Join(f.PreferredCountries, ", ")
		if where == "" {
			where = f.Country
		}
		return fmt.Sprintf(
			"Great — I have what I need for %s (%s) roles in %s. Choose a plan to start matching.",
			title, f.ExperienceLevel, where,
		)
	}
	// Not ready: model may ask a sensible next question; still prefer our
	// structured follow-up when the model invents readiness or is vague.
	if llmReply != "" && looksLikeAskingForMore(llmReply) && !looksLikeFalseReady(llmReply) {
		// If the model already asks for the top missing field, keep its wording.
		if replyTargetsMissing(llmReply, missing[0]) {
			return llmReply
		}
	}
	// Guided harness: acknowledge known signals + ask next gap with why.
	return placement.GuidedFollowUp(toPlacementFields(f))
}

func looksLikeAskingForMore(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "?") ||
		strings.Contains(low, "which ") ||
		strings.Contains(low, "what ") ||
		strings.Contains(low, "where ") ||
		strings.Contains(low, "could you") ||
		strings.Contains(low, "can you") ||
		strings.Contains(low, "please ")
}

func looksLikeFalseReady(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "all set") ||
		strings.Contains(low, "choose a plan") ||
		strings.Contains(low, "pick a plan") ||
		strings.Contains(low, "everything i need") ||
		strings.Contains(low, "ready to match")
}

// replyTargetsMissing is a soft check that the model is asking about the
// highest-priority gap (so we can keep natural LLM wording).
func replyTargetsMissing(reply, missingKey string) bool {
	low := strings.ToLower(reply)
	switch missingKey {
	case "target_job_title":
		return strings.Contains(low, "role") || strings.Contains(low, "title") || strings.Contains(low, "position")
	case "capabilities":
		return strings.Contains(low, "cv") || strings.Contains(low, "resume") || strings.Contains(low, "experience")
	case "job_types":
		return strings.Contains(low, "full-time") || strings.Contains(low, "job type") || strings.Contains(low, "contract")
	case "salary_expectation":
		return strings.Contains(low, "salary") || strings.Contains(low, "pay") || strings.Contains(low, "compensation")
	case "preferred_countries":
		return strings.Contains(low, "countr") || strings.Contains(low, "where") || strings.Contains(low, "location")
	case "experience_level":
		return strings.Contains(low, "experience") || strings.Contains(low, "senior") || strings.Contains(low, "level")
	default:
		return true
	}
}

// ─── Heuristic extraction (full user corpus) ───────────────────────────────

func heuristicExtract(msg string) onboardingChatFields {
	low := strings.ToLower(msg)
	var f onboardingChatFields

	// Experience — prefer longer/more specific tokens.
	switch {
	case containsAny(low, "executive", "c-level", "cxo", "director", " vp "):
		f.ExperienceLevel = "executive"
	case containsAny(low, "principal", "staff engineer", " lead ", "tech lead"):
		f.ExperienceLevel = "lead"
	case containsAny(low, "senior", "sr.", "sr "):
		f.ExperienceLevel = "senior"
	case containsAny(low, "mid-level", "mid level", "intermediate"):
		f.ExperienceLevel = "mid"
	case containsAny(low, "junior", "jr.", "jr "):
		f.ExperienceLevel = "junior"
	case containsAny(low, "entry-level", "entry level", "intern", "graduate", "new grad"):
		f.ExperienceLevel = "entry"
	default:
		// "7 years" → senior-ish
		if n := yearsOfExperience(low); n >= 10 {
			f.ExperienceLevel = "lead"
		} else if n >= 5 {
			f.ExperienceLevel = "senior"
		} else if n >= 2 {
			f.ExperienceLevel = "mid"
		} else if n == 1 {
			f.ExperienceLevel = "junior"
		}
	}

	switch {
	case containsAny(low, "actively looking", "actively seeking", "job hunting", "open to work"):
		f.JobSearchStatus = "actively_looking"
	case containsAny(low, "open to offer", "open to opportunities", "passive"):
		f.JobSearchStatus = "open_to_offers"
	case containsAny(low, "casually", "just browsing", "not actively"):
		f.JobSearchStatus = "casually_browsing"
	}

	var types []string
	if containsAny(low, "full-time", "full time") {
		types = append(types, "Full-time")
	}
	if containsAny(low, "part-time", "part time") {
		types = append(types, "Part-time")
	}
	if containsAny(low, "contract", "contractor") {
		types = append(types, "Contract")
	}
	if containsAny(low, "freelance") {
		types = append(types, "Freelance")
	}
	if containsAny(low, "internship", "intern ") {
		types = append(types, "Internship")
	}
	if containsAny(low, "remote") && len(types) == 0 {
		types = append(types, "Full-time")
	}
	f.JobTypes = normalizeJobTypes(types)

	var regions []string
	for needle, canon := range allowedRegions {
		if strings.Contains(low, needle) {
			regions = append(regions, canon)
		}
	}
	f.PreferredRegions = normalizeRegions(regions)

	var langs []string
	for _, l := range []string{"English", "French", "Arabic", "Swahili", "Portuguese", "Spanish", "German", "Mandarin"} {
		if strings.Contains(low, strings.ToLower(l)) {
			langs = append(langs, l)
		}
	}
	f.PreferredLanguages = normalizeLanguages(langs)

	countryHints := []struct {
		needle string
		code   string
	}{
		{"kenya", "KE"}, {"nairobi", "KE"},
		{"uganda", "UG"}, {"kampala", "UG"},
		{"nigeria", "NG"}, {"lagos", "NG"}, {"abuja", "NG"},
		{"ghana", "GH"}, {"accra", "GH"},
		{"south africa", "ZA"}, {"johannesburg", "ZA"}, {"cape town", "ZA"},
		{"united states", "US"}, {"u.s.a", "US"}, {"usa", "US"},
		{"united kingdom", "GB"}, {"london", "GB"},
		{"germany", "DE"}, {"france", "FR"}, {"india", "IN"},
		{"philippines", "PH"}, {"rwanda", "RW"}, {"tanzania", "TZ"},
		{"ethiopia", "ET"}, {"egypt", "EG"}, {"morocco", "MA"},
	}
	// Word-boundary-ish: avoid matching "ke" inside other words by requiring
	// longer needles first (already ordered). Collect all opportunity countries.
	var foundCountries []string
	seenC := map[string]bool{}
	for _, h := range countryHints {
		if strings.Contains(low, h.needle) && !seenC[h.code] {
			seenC[h.code] = true
			foundCountries = append(foundCountries, h.code)
		}
	}
	if len(foundCountries) > 0 {
		f.Country = foundCountries[0]
		f.PreferredCountries = foundCountries
	}

	// LinkedIn URL or handle.
	f.LinkedIn = extractLinkedIn(msg)

	// Salary: "$80k", "KES 200000", "80000-120000 USD"
	if min, max, cur := extractSalary(msg); min != nil || max != nil {
		f.SalaryMin = min
		f.SalaryMax = max
		f.Currency = cur
	}

	f.TargetJobTitle = guessTitle(msg)
	// Only treat the text as capabilities when it looks like a CV/work history —
	// never store short preference answers as a fake resume.
	if looksLikeCV(msg) {
		f.ExtraInfo = truncateRunes(msg, 8000)
	}
	return f
}

func extractLinkedIn(msg string) string {
	low := strings.ToLower(msg)
	// Full URL
	if i := strings.Index(low, "linkedin.com/in/"); i >= 0 {
		rest := msg[i:]
		// trim trailing punctuation/space
		end := len(rest)
		for j, r := range rest {
			if r == ' ' || r == '\n' || r == ',' || r == ';' || r == ')' || r == '>' {
				end = j
				break
			}
		}
		return normalizeLinkedIn(rest[:end])
	}
	// "linkedin: handle" / "linkedin handle"
	for _, prefix := range []string{"linkedin:", "linkedin ", "my linkedin is ", "linkedin is "} {
		if i := strings.Index(low, prefix); i >= 0 {
			rest := strings.TrimSpace(msg[i+len(prefix):])
			// first token
			for _, sep := range []string{" ", "\n", ",", ";"} {
				if j := strings.Index(rest, sep); j > 0 {
					rest = rest[:j]
				}
			}
			return normalizeLinkedIn(rest)
		}
	}
	return ""
}

func normalizeLinkedIn(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimRight(s, "/.")
	low := strings.ToLower(s)
	if strings.Contains(low, "linkedin.com/in/") {
		return "https://" + strings.TrimPrefix(low, "https://")
	}
	// bare handle
	s = strings.TrimPrefix(s, "@")
	if len(s) < 2 || len(s) > 100 {
		return ""
	}
	// reject if it looks like a sentence
	if strings.Contains(s, " ") {
		return ""
	}
	return s
}

func normalizeCountryList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range in {
		code := normalizeCountry(c)
		if code == "" {
			// Try full name via heuristic table
			code = countryCodeFromName(c)
		}
		if code == "" || !looksLikeCountry(code) || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out
}

func countryCodeFromName(s string) string {
	low := strings.ToLower(strings.TrimSpace(s))
	hints := map[string]string{
		"kenya": "KE", "uganda": "UG", "nigeria": "NG", "ghana": "GH",
		"south africa": "ZA", "united states": "US", "usa": "US", "us": "US",
		"united kingdom": "GB", "uk": "GB", "germany": "DE", "france": "FR",
		"india": "IN", "philippines": "PH", "rwanda": "RW", "tanzania": "TZ",
		"ethiopia": "ET", "egypt": "EG", "morocco": "MA",
	}
	if c, ok := hints[low]; ok {
		return c
	}
	return normalizeCountry(s)
}

func extractSalary(msg string) (min, max *float64, currency string) {
	// Patterns: $80k-120k, USD 80000, KES 200,000+, 80k to 120k USD
	replacements := strings.NewReplacer(",", "", "–", "-", "—", "-")
	clean := replacements.Replace(msg)
	low := strings.ToLower(clean)

	// Find currency token near numbers.
	cur := ""
	for _, c := range []string{"usd", "eur", "gbp", "kes", "ngn", "zar", "ghs", "aed", "inr"} {
		if strings.Contains(low, c) {
			cur = strings.ToUpper(c)
			break
		}
	}
	if strings.Contains(low, "$") && cur == "" {
		cur = "USD"
	}

	// Collect number(+k) tokens with optional range.
	type num struct {
		v float64
		i int
	}
	var nums []num
	for i := 0; i < len(clean); {
		if clean[i] < '0' || clean[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(clean) && clean[j] >= '0' && clean[j] <= '9' {
			j++
		}
		n := 0.0
		for _, ch := range clean[i:j] {
			n = n*10 + float64(ch-'0')
		}
		k := j
		// skip spaces
		for k < len(clean) && clean[k] == ' ' {
			k++
		}
		if k < len(clean) && (clean[k] == 'k' || clean[k] == 'K') {
			n *= 1000
			k++
		}
		// Ignore years like "5 years" / tiny numbers without k
		rest := strings.ToLower(strings.TrimSpace(clean[k:]))
		if strings.HasPrefix(rest, "year") || strings.HasPrefix(rest, "yr") {
			i = j
			continue
		}
		if n >= 1000 { // salary floor
			nums = append(nums, num{v: n, i: i})
		}
		i = j
	}
	if len(nums) == 0 {
		return nil, nil, ""
	}
	// Prefer two numbers that look like a range (close in the string).
	a, b := nums[0].v, nums[0].v
	if len(nums) >= 2 {
		b = nums[1].v
	}
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	min = &lo
	if hi != lo {
		max = &hi
	} else {
		max = &lo
	}
	if cur == "" {
		cur = "USD"
	}
	return min, max, cur
}

func yearsOfExperience(low string) int {
	// crude: "7 years" / "7+ years"
	for i := 0; i < len(low)-5; i++ {
		if low[i] >= '1' && low[i] <= '9' {
			n := int(low[i] - '0')
			j := i + 1
			if j < len(low) && low[j] >= '0' && low[j] <= '9' {
				n = n*10 + int(low[j]-'0')
				j++
			}
			rest := strings.TrimSpace(low[j:])
			if strings.HasPrefix(rest, "+") {
				rest = strings.TrimSpace(rest[1:])
			}
			if strings.HasPrefix(rest, "year") {
				return n
			}
		}
	}
	return 0
}

func containsAny(low string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

func guessTitle(msg string) string {
	low := strings.ToLower(msg)
	markers := []string{
		"i am a ", "i'm a ", "i am an ", "i'm an ",
		"i work as a ", "i work as an ", "work as a ", "work as an ",
		"role as a ", "role as an ",
		"target role is ", "my role is ", "title: ", "position: ",
		"seeking a ", "seeking an ",
		"looking for a ", "looking for an ",
		"want a ", "want an ", "need a ", "need an ",
	}
	for _, m := range markers {
		if i := strings.Index(low, m); i >= 0 {
			rest := strings.TrimSpace(msg[i+len(m):])
			for _, sep := range []string{"\n", ".", ",", ";", " in ", " based", " with ", " who ", " looking", " for full", " for part", " roles", " role"} {
				if j := strings.Index(strings.ToLower(rest), sep); j > 0 {
					rest = rest[:j]
				}
			}
			rest = collapseSpace(rest)
			// "looking for a full-time remote roles" is junk — not a title.
			if plausibleTitle(rest) && !junkTitle(rest) && jobishTitle(rest) {
				return rest
			}
			// Allow non-jobish short titles only from explicit "title:" style markers.
			if (m == "title: " || m == "position: " || m == "target role is " || m == "my role is ") &&
				plausibleTitle(rest) && !junkTitle(rest) {
				return rest
			}
		}
	}
	// "{Job title} looking for …" e.g. "Software engineer looking for remote work"
	if jobishTitle(msg) {
		for _, sep := range []string{" looking for", " seeking", " interested in", " open to"} {
			if i := strings.Index(low, sep); i > 3 {
				cand := collapseSpace(msg[:i])
				// Take last clause if multi-sentence.
				if j := strings.LastIndexAny(cand, ".\n"); j >= 0 {
					cand = collapseSpace(cand[j+1:])
				}
				// Strip leading "I am a" already handled; strip "Hi I'm"
				for _, p := range []string{"hi ", "hello ", "hey "} {
					if strings.HasPrefix(strings.ToLower(cand), p) {
						cand = collapseSpace(cand[len(p):])
					}
				}
				if plausibleTitle(cand) && !junkTitle(cand) && jobishTitle(cand) && len(cand) <= 80 {
					return cand
				}
			}
		}
	}
	// CV-style first lines
	for _, line := range strings.Split(msg, "\n") {
		line = collapseSpace(line)
		if len(line) < 4 || len(line) > 80 {
			continue
		}
		if strings.Contains(line, "@") || strings.Contains(strings.ToLower(line), "http") {
			continue
		}
		lowLine := strings.ToLower(line)
		if strings.HasPrefix(lowLine, "curriculum") || strings.HasPrefix(lowLine, "resume") ||
			strings.HasPrefix(lowLine, "summary") || strings.HasPrefix(lowLine, "objective") {
			continue
		}
		if jobishTitle(line) && !junkTitle(line) {
			return line
		}
	}
	return ""
}

func jobishTitle(s string) bool {
	low := strings.ToLower(s)
	for _, w := range []string{
		"engineer", "developer", "designer", "manager", "analyst", "director",
		"officer", "specialist", "consultant", "architect", "scientist", "nurse",
		"teacher", "accountant", "marketer", "product", "sales", "ops",
	} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

func junkTitle(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	if low == "" {
		return true
	}
	for _, p := range []string{
		"full-time", "full time", "part-time", "part time", "remote", "hybrid",
		"contract", "roles", "role", "jobs", "job", "opportunities", "opportunity",
		"work", "position", "looking", "seeking",
	} {
		if low == p || strings.HasPrefix(low, p+" ") {
			return true
		}
	}
	return false
}

func plausibleTitle(s string) bool {
	s = collapseSpace(s)
	if len(s) < 2 || len(s) > 80 {
		return false
	}
	// Must contain at least one letter.
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

func looksLikeCountry(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 2 && unicode.IsLetter(rune(s[0])) && unicode.IsLetter(rune(s[1])) {
		return true
	}
	// Known full names that normalizeCountry keeps as-is if unknown —
	// reject pure region tokens.
	low := strings.ToLower(s)
	for _, bad := range []string{"remote", "africa", "europe", "asia", "anywhere", "worldwide", "global"} {
		if low == bad {
			return false
		}
	}
	return len(s) >= 2
}

func normalizeExperience(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "entry", "entry-level", "entry level", "intern", "internship", "graduate", "new grad":
		return "entry"
	case "junior", "jr", "jr.":
		return "junior"
	case "mid", "mid-level", "mid level", "middle", "intermediate":
		return "mid"
	case "senior", "sr", "sr.":
		return "senior"
	case "lead", "staff", "principal":
		return "lead"
	case "executive", "director", "vp", "c-level", "cxo":
		return "executive"
	default:
		return ""
	}
}

func normalizeSearchStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "actively_looking", "actively looking", "active", "open to work":
		return "actively_looking"
	case "open_to_offers", "open to offers", "open", "passive":
		return "open_to_offers"
	case "casually_browsing", "casually browsing", "browsing":
		return "casually_browsing"
	default:
		return ""
	}
}

func normalizeCountry(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) == 2 {
		return strings.ToUpper(s)
	}
	aliases := map[string]string{
		"kenya": "KE", "uganda": "UG", "nigeria": "NG", "ghana": "GH",
		"south africa": "ZA", "united states": "US", "usa": "US", "u.s.": "US", "u.s.a.": "US",
		"united kingdom": "GB", "uk": "GB", "great britain": "GB",
		"germany": "DE", "france": "FR", "india": "IN", "philippines": "PH",
		"brazil": "BR", "rwanda": "RW", "tanzania": "TZ", "ethiopia": "ET",
		"egypt": "EG", "morocco": "MA",
	}
	if c, ok := aliases[strings.ToLower(s)]; ok {
		return c
	}
	return s
}

func normalizeJobTypes(in []string) []string {
	var out []string
	for _, s := range in {
		k := strings.ToLower(strings.TrimSpace(s))
		if canon, ok := allowedJobTypes[k]; ok {
			out = append(out, canon)
			continue
		}
		// Already canonical?
		for _, v := range allowedJobTypes {
			if strings.EqualFold(s, v) {
				out = append(out, v)
				break
			}
		}
	}
	return uniqueNonEmpty(out)
}

func normalizeRegions(in []string) []string {
	var out []string
	for _, s := range in {
		k := strings.ToLower(strings.TrimSpace(s))
		if canon, ok := allowedRegions[k]; ok {
			out = append(out, canon)
			continue
		}
		for _, v := range allowedRegions {
			if strings.EqualFold(s, v) {
				out = append(out, v)
				break
			}
		}
	}
	return uniqueNonEmpty(out)
}

func normalizeLanguages(in []string) []string {
	known := map[string]string{
		"english": "English", "french": "French", "arabic": "Arabic",
		"swahili": "Swahili", "kiswahili": "Swahili",
		"portuguese": "Portuguese", "spanish": "Spanish",
		"german": "German", "mandarin": "Mandarin", "chinese": "Mandarin",
	}
	var out []string
	for _, s := range in {
		k := strings.ToLower(strings.TrimSpace(s))
		if canon, ok := known[k]; ok {
			out = append(out, canon)
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			// Title-case unknown language names lightly.
			out = append(out, s)
		}
	}
	return uniqueNonEmpty(out)
}

func regionForCountry(cc string) []string {
	c := strings.ToUpper(strings.TrimSpace(cc))
	africa := map[string]struct{}{
		"KE": {}, "UG": {}, "NG": {}, "GH": {}, "ZA": {}, "RW": {}, "TZ": {}, "ET": {}, "EG": {}, "MA": {},
		"SN": {}, "CI": {}, "CM": {}, "ZM": {}, "ZW": {}, "BW": {}, "MW": {},
	}
	if _, ok := africa[c]; ok {
		return []string{"Africa"}
	}
	switch c {
	case "US", "CA", "MX":
		return []string{"North America"}
	case "GB", "DE", "FR", "NL", "IE", "ES", "IT", "SE", "NO", "CH", "BE", "AT", "PT", "PL":
		return []string{"Europe"}
	case "IN", "PH", "SG", "JP", "CN", "AE", "SA", "KR", "ID", "MY":
		return []string{"Asia"}
	case "BR", "AR", "CL", "CO", "PE":
		return []string{"South America"}
	case "AU", "NZ":
		return []string{"Oceania"}
	default:
		return []string{"Anywhere"}
	}
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		k := strings.ToLower(s)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// extractJSONObject strips markdown fences and leading prose so we can
// decode model output that wraps JSON in ```json … ``` or chatter.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.Index(s, "{"); i >= 0 {
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return strings.TrimSpace(s[i : j+1])
				}
			}
		}
		return strings.TrimSpace(s[i:])
	}
	return s
}
