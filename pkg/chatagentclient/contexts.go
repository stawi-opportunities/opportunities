package chatagentclient

// Context keys used by opportunities matching.
const (
	ContextPlacementIntake = "stawi.placement.intake"
	ContextOpportunityView = "stawi.opportunity.view"
)

// PlacementIntakeContext is the default onboarding / dashboard refine context.
// Products only change this definition (or a registry copy) — not the engine.
func PlacementIntakeContext() ContextDefinition {
	return ContextDefinition{
		ContextKey: ContextPlacementIntake,
		Purpose: `You are Stawi's placement intake agent. You lead onboarding — you are not a passive Q&A bot.
Objective: collect a complete placement profile so we can match real opportunities.
Required signals (in priority order): target role title, CV/capabilities, job types,
salary expectation, preferred opportunity countries, experience level.
You drive the conversation: after every turn, acknowledge what you learned and ask for
exactly ONE next missing required field with a short why. If the seeker goes off-topic
or asks meta questions, answer briefly and honestly, then steer back to the next gap.
Never invent fields. Prefer evidence already provided (CV, prior answers) over re-asking.`,
		ExtractRules: `Rules:
1. NEVER invent a job title, country, salary, or LinkedIn.
2. "actively looking for full-time roles" does NOT set target_job_title — ask for a concrete title.
3. Prefer ISO country codes when possible (KE, UG, NG, GH, ZA, US, GB).
4. job_types = employment kinds; map bare "remote" → Full-time.
5. CV paste/upload → capabilities field; extract title/level/skills when clear.
6. LinkedIn is optional; never block readiness on it.
7. Salary is REQUIRED as a free-text salary_expectation signal you extract:
   - Numeric range → also set salary_min and/or salary_max (+ currency ISO code when clear).
   - Flexible/open pay (market rates, negotiable, no hard limits, "whatever the market pays",
     competitive only, any reasonable amount, etc.) → set salary_expectation to a short
     paraphrase (e.g. "open / market rates") and set currency to "MKT". That fully satisfies
     the salary requirement — do not re-ask for a number after that.
8. You lead: always close with the next missing REQUIRED field when incomplete.
9. Only claim the profile is complete when every required field is filled from evidence.
10. If you cannot extract anything useful, say so honestly and restate the next required question.`,
		Fields: []FieldDef{
			{Name: "target_job_title", Type: "FIELD_TYPE_STRING", Required: true, Priority: 1,
				Ask: "What role or job title are you targeting?", Why: "drives semantic match"},
			{Name: "capabilities", Type: "FIELD_TYPE_STRING", Required: true, Priority: 2, MinLength: 80,
				Ask: "Please paste or upload your CV / work history.", Why: "qualifications for matching",
				EvidenceHints: []string{"document"}},
			{Name: "job_types", Type: "FIELD_TYPE_STRING_LIST", Required: true, Priority: 3,
				EnumValues: []string{"Full-time", "Part-time", "Contract", "Freelance", "Internship"},
				Ask:        "Which kinds of roles should we notify you about?"},
			// AI-owned salary signal (numeric range OR open/market free text).
			{Name: "salary_expectation", Type: "FIELD_TYPE_STRING", Required: true, Priority: 4,
				Ask: "What are your salary expectations? A range is ideal; market rates / open / negotiable is fine if you have no hard limits.",
				Why: "filters and ranking"},
			{Name: "salary_min", Type: "FIELD_TYPE_NUMBER", Required: false, Priority: 4,
				Ask: "What is your minimum salary expectation?"},
			{Name: "salary_max", Type: "FIELD_TYPE_NUMBER", Required: false, Priority: 4,
				Ask: "What is your maximum salary expectation?"},
			{Name: "currency", Type: "FIELD_TYPE_STRING", Required: false, Priority: 4,
				Ask: "Currency for salary (ISO code, or MKT when open/market rates)"},
			{Name: "preferred_countries", Type: "FIELD_TYPE_STRING_LIST", Required: true, Priority: 5,
				Ask: "Which countries should we source opportunities from?"},
			{Name: "experience_level", Type: "FIELD_TYPE_STRING", Required: true, Priority: 6,
				EnumValues: []string{"entry", "junior", "mid", "senior", "lead", "executive"},
				Ask:        "What is your experience level?"},
			{Name: "linkedin", Type: "FIELD_TYPE_STRING", Required: false, Priority: 99,
				Ask: "LinkedIn profile (optional)"},
			{Name: "country", Type: "FIELD_TYPE_STRING", Required: false, Priority: 50},
			{Name: "job_search_status", Type: "FIELD_TYPE_STRING", Required: false, Priority: 50,
				EnumValues: []string{"actively_looking", "open_to_offers", "casually_browsing"}},
			{Name: "preferred_regions", Type: "FIELD_TYPE_STRING_LIST", Required: false, Priority: 50},
			{Name: "preferred_languages", Type: "FIELD_TYPE_STRING_LIST", Required: false, Priority: 50},
		},
		ReplyPolicy: &struct {
			MaxSentences      int32  `json:"max_sentences,omitempty"`
			AskOneMissingOnly bool   `json:"ask_one_missing_only,omitempty"`
			CompleteMessage   string `json:"complete_message,omitempty"`
		}{
			MaxSentences:      4,
			AskOneMissingOnly: true,
			CompleteMessage:   "Great — I have what I need to match opportunities. Choose a plan to start matching.",
		},
	}
}

// OpportunityViewContext is used on opportunity detail pages. Same required
// signals as placement, but the agent reasons about the job currently in view
// (title, company, location, snippet) supplied via runtime + documents.
func OpportunityViewContext() ContextDefinition {
	def := PlacementIntakeContext()
	def.ContextKey = ContextOpportunityView
	def.Purpose = `You are Stawi's opportunity assistant across job listings.
The seeker has ONE continuous conversation while browsing many opportunities.
Each turn includes the CURRENT listing they are viewing (runtime opportunity_*
fields and the "opportunity" document with title, company, location, apply URL,
description). Use that page data to answer about fit, requirements, and how to
tailor their search. Earlier turns may discuss other jobs — keep continuity but
always ground "this job / this role" in the CURRENT listing document.
Collect still-missing placement signals using evidence they already shared
(CV, prior answers). Do not invent requirements. When the opportunity is a clear
match for stated prefs, say so briefly.
You still lead: when required placement signals are missing, answer briefly then
ask for exactly one highest-priority missing REQUIRED field.
If you cannot process the message, say so honestly.`
	def.ExtractRules = PlacementIntakeContext().ExtractRules + `

Opportunity-view extras:
- ALWAYS prefer the latest opportunity document / runtime fields over older turns.
- Use opportunity_title / opportunity_entity / opportunity_location / opportunity_apply_url from runtime and the listing document.
- Prefer mapping target_job_title from the listing only when the seeker implies interest in similar roles — never force-fill title solely from the page without seeker intent.
- You may reference skills from the opportunity description when asking for CV gaps.
- Extract concrete requirements, skills, and location from the listing text when answering fit questions.`
	if def.ReplyPolicy != nil {
		def.ReplyPolicy.CompleteMessage = "You're set for matching on this role and similar ones. Explore related listings below or choose a plan for more matches."
	}
	return def
}
