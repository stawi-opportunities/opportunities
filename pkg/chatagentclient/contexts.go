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
		Purpose: `You are Stawi's placement intake agent for opportunity seekers.
Collect a placement profile: (A) qualifications from CV/work history and
(B) preferences (role, level, job types, salary, markets). Incomplete profiles
produce poor matches. Prefer evidence already provided (CV, prior answers)
over re-asking.`,
		ExtractRules: `Rules:
1. NEVER invent a job title, country, salary, or LinkedIn.
2. "actively looking for full-time roles" does NOT set target_job_title.
3. Prefer ISO country codes when possible (KE, UG, NG, GH, ZA, US, GB).
4. job_types = employment kinds; map bare "remote" → Full-time.
5. CV paste/upload → capabilities field; extract title/level/skills when clear.
6. LinkedIn is optional; never block readiness on it.
7. salary_min and/or salary_max with currency when stated.`,
		Fields: []FieldDef{
			{Name: "target_job_title", Type: "FIELD_TYPE_STRING", Required: true, Priority: 1,
				Ask: "What role or job title are you targeting?", Why: "drives semantic match"},
			{Name: "capabilities", Type: "FIELD_TYPE_STRING", Required: true, Priority: 2, MinLength: 80,
				Ask: "Please paste or upload your CV / work history.", Why: "qualifications for matching",
				EvidenceHints: []string{"document"}},
			{Name: "job_types", Type: "FIELD_TYPE_STRING_LIST", Required: true, Priority: 3,
				EnumValues: []string{"Full-time", "Part-time", "Contract", "Freelance", "Internship"},
				Ask:        "Which kinds of roles should we notify you about?"},
			{Name: "salary_min", Type: "FIELD_TYPE_NUMBER", Required: false, Priority: 4,
				Ask: "What is your minimum salary expectation?"},
			{Name: "salary_max", Type: "FIELD_TYPE_NUMBER", Required: false, Priority: 4,
				Ask: "What is your maximum salary expectation?"},
			{Name: "currency", Type: "FIELD_TYPE_STRING", Required: false, Priority: 4},
			// Readiness for salary uses composite check in matching; required-or in assess is split.
			// We treat salary via preferred composite in matching after map.
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
		}{MaxSentences: 3, AskOneMissingOnly: true, CompleteMessage: "Great — I have what I need. Choose a plan to start matching."},
	}
}

// OpportunityViewContext is used on opportunity detail pages. Same required
// signals as placement, but the agent reasons about the job currently in view
// (title, company, location, snippet) supplied via runtime + documents.
func OpportunityViewContext() ContextDefinition {
	def := PlacementIntakeContext()
	def.ContextKey = ContextOpportunityView
	def.Purpose = `You are Stawi's opportunity assistant on a listing page.
The seeker is viewing a specific opportunity (see runtime opportunity_* fields
and the opportunity document). Help them understand fit, what is missing for
strong matching, and collect any still-missing placement signals using
evidence they already shared (CV, prior answers). Do not invent requirements.
When the opportunity is a clear match for stated prefs, say so briefly.
Still only ask for the single highest-priority missing REQUIRED field when not ready.`
	def.ExtractRules = PlacementIntakeContext().ExtractRules + `

Opportunity-view extras:
- Use opportunity_title / opportunity_entity / opportunity_location from runtime as context.
- Prefer mapping target_job_title from the listing only when the seeker implies interest in similar roles — never force-fill title solely from the page without seeker intent.
- You may reference skills from the opportunity description when asking for CV gaps.`
	if def.ReplyPolicy != nil {
		def.ReplyPolicy.CompleteMessage = "You're set for matching on this role and similar ones. Explore related listings below or choose a plan for more matches."
	}
	return def
}
