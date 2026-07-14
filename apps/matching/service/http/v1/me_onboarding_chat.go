package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

// OnboardingChatLLM is the minimal surface the chat wizard needs from the
// extraction package. *extraction.Extractor satisfies it via Complete().
// Nil disables AI and falls back to a deterministic heuristic parser.
type OnboardingChatLLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// OnboardingChatDeps wires the chat turn handler.
type OnboardingChatDeps struct {
	LLM OnboardingChatLLM // optional
}

// onboardingChatMessage is one turn in the conversation.
type onboardingChatMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// onboardingChatFields is the structured profile we fill during chat.
// Mirrors OnboardingDraftFields / onboardPayload (without plan/terms).
type onboardingChatFields struct {
	TargetJobTitle     string   `json:"target_job_title,omitempty"`
	ExperienceLevel    string   `json:"experience_level,omitempty"`
	JobSearchStatus    string   `json:"job_search_status,omitempty"`
	SalaryMin          *float64 `json:"salary_min,omitempty"`
	SalaryMax          *float64 `json:"salary_max,omitempty"`
	Currency           string   `json:"currency,omitempty"`
	PreferredRegions   []string `json:"preferred_regions,omitempty"`
	PreferredTimezones []string `json:"preferred_timezones,omitempty"`
	PreferredLanguages []string `json:"preferred_languages,omitempty"`
	JobTypes           []string `json:"job_types,omitempty"`
	Country            string   `json:"country,omitempty"`
	ExtraInfo          string   `json:"extra_info,omitempty"`
}

// onboardingChatRequest is the body of POST /me/onboarding/chat.
type onboardingChatRequest struct {
	Message string                  `json:"message"`
	History []onboardingChatMessage `json:"history,omitempty"`
	Draft   onboardingChatFields    `json:"draft,omitempty"`
}

// onboardingChatResponse is returned after each turn.
type onboardingChatResponse struct {
	Reply   string               `json:"reply"`
	Fields  onboardingChatFields `json:"fields"`
	Missing []string             `json:"missing"`
	Ready   bool                 `json:"ready"`
}

// requiredChatFields are the profile keys that must be non-empty before plan selection.
var requiredChatFields = []string{
	"target_job_title",
	"experience_level",
	"job_search_status",
	"preferred_regions",
	"country",
	"preferred_languages",
	"job_types",
}

// OnboardingChatHandler serves POST /me/onboarding/chat — one conversational
// turn. Merges the user's free-text (and optional pasted CV) into a structured
// draft via the LLM when configured, otherwise a lightweight heuristic.
func OnboardingChatHandler(deps OnboardingChatDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpmw.ProblemJSON(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "use POST")
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

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
		if msg == "" {
			httpmw.ProblemJSON(w, http.StatusBadRequest,
				"empty_message", "message is required")
			return
		}

		merged := in.Draft
		// Always keep free text so onboard has "extra_info" / CV substitute.
		if merged.ExtraInfo == "" {
			merged.ExtraInfo = msg
		} else {
			merged.ExtraInfo = strings.TrimSpace(merged.ExtraInfo + "\n\n" + msg)
		}

		var reply string
		if deps.LLM != nil {
			extracted, llmReply, llmErr := chatExtractWithLLM(ctx, deps.LLM, in.History, msg, merged)
			if llmErr != nil {
				log.WithError(llmErr).Warn("me/onboarding/chat: LLM failed; using heuristic")
				merged = mergeChatFields(merged, heuristicExtract(msg))
				reply = followUpReply(merged)
			} else {
				merged = mergeChatFields(merged, extracted)
				if strings.TrimSpace(llmReply) != "" {
					reply = llmReply
				} else {
					reply = followUpReply(merged)
				}
			}
		} else {
			merged = mergeChatFields(merged, heuristicExtract(msg))
			reply = followUpReply(merged)
		}

		missing := missingChatFields(merged)
		out := onboardingChatResponse{
			Reply:   reply,
			Fields:  merged,
			Missing: missing,
			Ready:   len(missing) == 0,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func chatExtractWithLLM(
	ctx context.Context,
	llm OnboardingChatLLM,
	history []onboardingChatMessage,
	userMsg string,
	prior onboardingChatFields,
) (onboardingChatFields, string, error) {
	priorJSON, _ := json.Marshal(prior)
	var hist strings.Builder
	for _, m := range history {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		// Cap history so we don't blow the context window.
		if hist.Len() > 12_000 {
			break
		}
		fmt.Fprintf(&hist, "%s: %s\n", role, truncateRunes(m.Content, 4000))
	}
	prompt := fmt.Sprintf(`You help candidates set job-search preferences for Stawi Opportunities.
Read the conversation and the latest user message (they may paste a CV or free-form goals).
Merge with the existing draft JSON and return ONLY a JSON object with this shape:
{
  "fields": {
    "target_job_title": string,
    "experience_level": "entry"|"junior"|"mid"|"senior"|"lead"|"executive",
    "job_search_status": "actively_looking"|"open_to_offers"|"casually_browsing",
    "salary_min": number|null,
    "salary_max": number|null,
    "currency": string (ISO 4217, e.g. "USD","KES"),
    "preferred_regions": string[],
    "preferred_timezones": string[],
    "preferred_languages": string[],
    "job_types": string[] (e.g. "Full-time","Remote","Contract"),
    "country": string (prefer ISO 3166-1 alpha-2 when clear, else country name),
    "extra_info": string (short summary of skills / background)
  },
  "reply": string (1-3 short sentences: confirm what you understood; if anything required is still missing, ask for ONLY the most important missing piece; if complete, say they're ready to pick a plan),
  "missing": string[] (subset of required keys still empty)
}

Required keys: target_job_title, experience_level, job_search_status, preferred_regions, country, preferred_languages, job_types.
Do not invent a country or title if the user never hinted; leave missing instead.
ISO countries: Kenya=KE, Uganda=UG, Nigeria=NG, Ghana=GH, South Africa=ZA, United States=US, UK=GB.

Existing draft:
%s

Conversation so far:
%s

Latest user message:
%s
`, string(priorJSON), hist.String(), truncateRunes(userMsg, 12000))

	raw, err := llm.Complete(ctx, prompt)
	if err != nil {
		return onboardingChatFields{}, "", err
	}
	// Tolerate fenced JSON.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var parsed struct {
		Fields  onboardingChatFields `json:"fields"`
		Reply   string               `json:"reply"`
		Missing []string             `json:"missing"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return onboardingChatFields{}, "", fmt.Errorf("decode llm json: %w", err)
	}
	_ = parsed.Missing // recompute server-side
	return parsed.Fields, parsed.Reply, nil
}

func mergeChatFields(base, overlay onboardingChatFields) onboardingChatFields {
	out := base
	if v := strings.TrimSpace(overlay.TargetJobTitle); v != "" {
		out.TargetJobTitle = v
	}
	if v := normalizeExperience(overlay.ExperienceLevel); v != "" {
		out.ExperienceLevel = v
	}
	if v := normalizeSearchStatus(overlay.JobSearchStatus); v != "" {
		out.JobSearchStatus = v
	}
	if overlay.SalaryMin != nil {
		out.SalaryMin = overlay.SalaryMin
	}
	if overlay.SalaryMax != nil {
		out.SalaryMax = overlay.SalaryMax
	}
	if v := strings.TrimSpace(overlay.Currency); v != "" {
		out.Currency = strings.ToUpper(v)
	}
	if len(overlay.PreferredRegions) > 0 {
		out.PreferredRegions = uniqueNonEmpty(overlay.PreferredRegions)
	}
	if len(overlay.PreferredTimezones) > 0 {
		out.PreferredTimezones = uniqueNonEmpty(overlay.PreferredTimezones)
	}
	if len(overlay.PreferredLanguages) > 0 {
		out.PreferredLanguages = uniqueNonEmpty(overlay.PreferredLanguages)
	}
	if len(overlay.JobTypes) > 0 {
		out.JobTypes = uniqueNonEmpty(overlay.JobTypes)
	}
	if v := strings.TrimSpace(overlay.Country); v != "" {
		out.Country = normalizeCountry(v)
	}
	if v := strings.TrimSpace(overlay.ExtraInfo); v != "" {
		out.ExtraInfo = v
	}
	// Defaults for search status when still empty after merge heuristics.
	if out.JobSearchStatus == "" && out.TargetJobTitle != "" {
		out.JobSearchStatus = "actively_looking"
	}
	return out
}

func missingChatFields(f onboardingChatFields) []string {
	var miss []string
	if strings.TrimSpace(f.TargetJobTitle) == "" {
		miss = append(miss, "target_job_title")
	}
	if normalizeExperience(f.ExperienceLevel) == "" {
		miss = append(miss, "experience_level")
	}
	if normalizeSearchStatus(f.JobSearchStatus) == "" {
		miss = append(miss, "job_search_status")
	}
	if len(uniqueNonEmpty(f.PreferredRegions)) == 0 {
		miss = append(miss, "preferred_regions")
	}
	if strings.TrimSpace(f.Country) == "" {
		miss = append(miss, "country")
	}
	if len(uniqueNonEmpty(f.PreferredLanguages)) == 0 {
		miss = append(miss, "preferred_languages")
	}
	if len(uniqueNonEmpty(f.JobTypes)) == 0 {
		miss = append(miss, "job_types")
	}
	return miss
}

func followUpReply(f onboardingChatFields) string {
	miss := missingChatFields(f)
	if len(miss) == 0 {
		return "Thanks — I have everything I need. Pick a plan below to start matching."
	}
	// Ask for one thing at a time.
	switch miss[0] {
	case "target_job_title":
		return "What role are you targeting? (e.g. Senior Software Engineer, Product Manager)"
	case "experience_level":
		return "What's your experience level — entry, junior, mid, senior, lead, or executive?"
	case "job_search_status":
		return "How actively are you looking — actively looking, open to offers, or casually browsing?"
	case "country":
		return "Which country are you based in or targeting? (e.g. Kenya, Nigeria, remote-friendly)"
	case "preferred_regions":
		return "Which regions should we search? (Anywhere, Africa, Europe, North America, …)"
	case "preferred_languages":
		return "Which languages should job posts be in? (English, French, Swahili, …)"
	case "job_types":
		return "What employment types work for you? (Full-time, Part-time, Contract, Freelance, Internship)"
	default:
		return "Could you share a bit more about the role and where you want to work?"
	}
}

// heuristicExtract is a best-effort offline parser used when the LLM is down.
func heuristicExtract(msg string) onboardingChatFields {
	low := strings.ToLower(msg)
	var f onboardingChatFields

	// Experience
	for _, lvl := range []string{"executive", "lead", "senior", "mid", "junior", "entry"} {
		if strings.Contains(low, lvl) {
			f.ExperienceLevel = lvl
			if lvl == "mid" && !strings.Contains(low, "mid-level") && !strings.Contains(low, "mid level") && !strings.Contains(low, " mid ") {
				// avoid matching "amid" etc. — already crude
			}
			break
		}
	}
	if strings.Contains(low, "intern") {
		f.ExperienceLevel = "entry"
	}

	// Search status
	switch {
	case strings.Contains(low, "actively looking"), strings.Contains(low, "actively seeking"):
		f.JobSearchStatus = "actively_looking"
	case strings.Contains(low, "open to offer"), strings.Contains(low, "open to opportunities"):
		f.JobSearchStatus = "open_to_offers"
	case strings.Contains(low, "casually"), strings.Contains(low, "just browsing"):
		f.JobSearchStatus = "casually_browsing"
	}

	// Job types
	var types []string
	for _, jt := range []struct{ needle, val string }{
		{"full-time", "Full-time"}, {"full time", "Full-time"},
		{"part-time", "Part-time"}, {"part time", "Part-time"},
		{"contract", "Contract"}, {"freelance", "Freelance"},
		{"internship", "Internship"}, {"remote", "Remote"},
	} {
		if strings.Contains(low, jt.needle) {
			types = append(types, jt.val)
		}
	}
	f.JobTypes = uniqueNonEmpty(types)

	// Regions
	var regions []string
	for _, r := range []string{"Anywhere", "Africa", "Europe", "North America", "South America", "Asia", "Oceania", "Middle East"} {
		if strings.Contains(low, strings.ToLower(r)) {
			regions = append(regions, r)
		}
	}
	if strings.Contains(low, "worldwide") || strings.Contains(low, "anywhere") {
		regions = append(regions, "Anywhere")
	}
	f.PreferredRegions = uniqueNonEmpty(regions)

	// Languages
	var langs []string
	for _, l := range []string{"English", "French", "Arabic", "Swahili", "Portuguese", "Spanish", "German", "Mandarin"} {
		if strings.Contains(low, strings.ToLower(l)) {
			langs = append(langs, l)
		}
	}
	if len(langs) == 0 && len(msg) > 40 {
		// Free-form English blob → assume English.
		langs = []string{"English"}
	}
	f.PreferredLanguages = uniqueNonEmpty(langs)

	// Country aliases
	countryHints := map[string]string{
		"kenya": "KE", "nairobi": "KE", "ke": "KE",
		"uganda": "UG", "kampala": "UG", "ug": "UG",
		"nigeria": "NG", "lagos": "NG", "ng": "NG",
		"ghana": "GH", "accra": "GH", "gh": "GH",
		"south africa": "ZA", "johannesburg": "ZA", "cape town": "ZA", "za": "ZA",
		"united states": "US", "usa": "US", "america": "US",
		"united kingdom": "GB", "uk": "GB", "london": "GB",
		"germany": "DE", "france": "FR", "india": "IN", "philippines": "PH",
	}
	for needle, code := range countryHints {
		if strings.Contains(low, needle) {
			f.Country = code
			break
		}
	}

	// Crude title: "looking for a X role" / "I am a X"
	f.TargetJobTitle = guessTitle(msg)
	f.ExtraInfo = truncateRunes(msg, 2000)
	return f
}

func guessTitle(msg string) string {
	low := strings.ToLower(msg)
	markers := []string{
		"looking for a ", "looking for an ", "looking for ",
		"seeking a ", "seeking an ", "seeking ",
		"target role is ", "role as a ", "role as an ",
		"i am a ", "i'm a ", "i am an ", "i'm an ",
		"work as a ", "work as an ",
	}
	for _, m := range markers {
		if i := strings.Index(low, m); i >= 0 {
			rest := strings.TrimSpace(msg[i+len(m):])
			// Take until punctuation or newline.
			for _, sep := range []string{"\n", ".", ",", ";", " in ", " based", " with ", " who "} {
				if j := strings.Index(strings.ToLower(rest), sep); j > 0 {
					rest = rest[:j]
				}
			}
			rest = strings.TrimSpace(rest)
			if len(rest) >= 2 && len(rest) <= 80 {
				return rest
			}
		}
	}
	return ""
}

func normalizeExperience(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "entry", "entry-level", "intern", "internship", "graduate":
		return "entry"
	case "junior", "jr":
		return "junior"
	case "mid", "mid-level", "middle", "intermediate":
		return "mid"
	case "senior", "sr":
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
	case "actively_looking", "actively looking", "active":
		return "actively_looking"
	case "open_to_offers", "open to offers", "open":
		return "open_to_offers"
	case "casually_browsing", "casually browsing", "browsing":
		return "casually_browsing"
	default:
		return ""
	}
}

func normalizeCountry(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 2 {
		return strings.ToUpper(s)
	}
	aliases := map[string]string{
		"kenya": "KE", "uganda": "UG", "nigeria": "NG", "ghana": "GH",
		"south africa": "ZA", "united states": "US", "usa": "US",
		"united kingdom": "GB", "uk": "GB", "germany": "DE", "france": "FR",
		"india": "IN", "philippines": "PH", "brazil": "BR", "rwanda": "RW",
		"tanzania": "TZ", "ethiopia": "ET", "egypt": "EG", "morocco": "MA",
	}
	if c, ok := aliases[strings.ToLower(s)]; ok {
		return c
	}
	return s
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
