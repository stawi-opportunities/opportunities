package eventsv1

// RecipeGenerateV1 is emitted when a source needs its extraction recipe
// synthesised for the first time — at onboarding (source_discovered_handler)
// or when an operator requests an explicit re-generation via the admin API.
// Consumed by RecipeGenerateHandler, which runs the Generator → Validator →
// Store pipeline. On success the source moves to "active"; after
// RECIPE_MAX_GEN_ATTEMPTS consecutive failures it moves to "needs_tuning".
//
// Wire format only — this event is not persisted in the Parquet log.
type RecipeGenerateV1 struct {
	// SourceID is the sources.id of the source that needs a recipe.
	SourceID string `json:"source_id"`

	// SampleURLs are detail-page URLs the generator should fetch and
	// learn the recipe from. When empty the handler falls back to the
	// source's BaseURL.
	SampleURLs []string `json:"sample_urls,omitempty"`

	// Reason is a short human-readable label for observability (e.g.
	// "onboarding", "manual_trigger"). Not acted on programmatically.
	Reason string `json:"reason,omitempty"`

	// Attempt is 1 for a fresh request; the generator handler bumps it
	// on each repair-loop iteration. When Attempt > RECIPE_MAX_GEN_ATTEMPTS
	// the handler transitions the source to "needs_tuning" instead of
	// re-emitting the event.
	Attempt int `json:"attempt,omitempty"`
}

// RecipeRegenerateV1 is emitted by the page-completed handler when drift is
// detected on a recipe-driven source (reject-rate or missing-required-field
// rate crosses RECIPE_REGEN_REJECT_RATE over a sliding window). Consumed by
// RecipeRegenerateHandler, which runs the same Generator → Validator → Store
// pipeline but performs an atomic swap so the old recipe keeps serving until
// the new one passes the validation gate. After RECIPE_MAX_REGEN_FAILURES
// consecutive failures the source is marked "needs_tuning" for operator review.
//
// Wire format only — this event is not persisted in the Parquet log.
type RecipeRegenerateV1 struct {
	// SourceID is the sources.id of the source whose recipe has drifted.
	SourceID string `json:"source_id"`

	// CurrentVersion is the version number of the recipe that triggered
	// regeneration. Carried so the handler can detect a race where a
	// previous regeneration already swapped in a newer version.
	CurrentVersion int `json:"current_version,omitempty"`

	// RejectRate is the windowed reject-rate that crossed the threshold,
	// recorded for observability and the operator UI.
	RejectRate float64 `json:"reject_rate,omitempty"`

	// Attempt is 1 for a fresh regeneration; bumped on each failure.
	// When Attempt > RECIPE_MAX_REGEN_FAILURES the handler stops
	// retrying and sets needs_tuning.
	Attempt int `json:"attempt,omitempty"`
}
