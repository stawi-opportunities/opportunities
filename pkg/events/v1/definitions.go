package eventsv1

import "time"

// TopicDefinitionsChanged is broadcast when an admin edits land. Loaders
// subscribe and call Invalidate on the matching cache entry — instant
// propagation without waiting for the 5-min R2 refresh tick.
const TopicDefinitionsChanged = "opportunities.definitions.changed.v1"

// DefinitionsChangedV1 is the payload. The Type/Name pair identifies
// the changed object; Action discriminates upsert vs delete vs a
// wildcard reload.
type DefinitionsChangedV1 struct {
	// Type matches definitions.Type strings (kind|prompt|connector|seed),
	// or "*" for a wildcard reload (force every loader to re-fetch
	// everything).
	Type string `json:"type"`
	// Name is the definition name (filename without .yaml), or empty/"*"
	// for wildcard.
	Name string `json:"name"`
	// Version is the R2 ETag of the new object, or "deleted" when the
	// underlying object was removed.
	Version string `json:"version"`
	// Action is one of: upsert | delete | reload.
	Action string `json:"action"`
	// ChangedAt is the UTC timestamp of the mutation.
	ChangedAt time.Time `json:"changed_at"`
	// ChangedBy is the operator subject (empty for system-initiated
	// reloads).
	ChangedBy string `json:"changed_by,omitempty"`
}
