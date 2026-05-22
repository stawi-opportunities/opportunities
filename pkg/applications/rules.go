package applications

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Rules is the per-candidate autonomy document. Stored as JSONB in
// match_rules.document, validated through ParseRules.
//
// Note: pointer-free zero values are the defaults — DefaultRules
// returns the canonical starting point.
type Rules struct {
	Version          int            `json:"version"`
	Enabled          bool           `json:"enabled"`
	MinScore         float64        `json:"min_score"`
	DailyCap         int            `json:"daily_cap"`
	WeeklyCap        int            `json:"weekly_cap"`
	Kinds            []string       `json:"kinds"`
	Countries        []string       `json:"countries,omitempty"`
	SalaryFloorUSD   int            `json:"salary_floor_usd,omitempty"`
	RemoteOnly       bool           `json:"remote_only,omitempty"`
	DismissAfterDays int            `json:"dismiss_after_days,omitempty"`
	Blocklist        Blocklist      `json:"blocklist,omitempty"`
	Autoapply        AutoapplyRules `json:"autoapply,omitempty"`
}

// Blocklist forbids specific companies or domains.
type Blocklist struct {
	Companies []string `json:"companies,omitempty"`
	Domains   []string `json:"domains,omitempty"`
}

// AutoapplyRules gates the browser extension's submission behavior.
// Distinct from Rules.Enabled so the user can keep matches flowing
// while pausing automated submissions.
type AutoapplyRules struct {
	Enabled         bool     `json:"enabled,omitempty"`
	RequireMinScore float64  `json:"require_min_score,omitempty"`
	Kinds           []string `json:"kinds,omitempty"`
}

// validKinds is the closed set of opportunity kinds this design
// supports. Extending this is intentionally a code change — bumping
// the rules `version` is the migration path.
var validKinds = map[string]bool{
	"job":         true,
	"scholarship": true,
	"funding":     true,
	"tender":      true,
	"deal":        true,
}

// DefaultRules returns the starting rule document for a brand-new
// candidate.
func DefaultRules() Rules {
	return Rules{
		Version:          1,
		Enabled:          true,
		MinScore:         0.5,
		DailyCap:         25,
		WeeklyCap:        100,
		Kinds:            []string{"job"},
		DismissAfterDays: 14,
	}
}

// ParseRules decodes the JSON document and validates it against the
// schema described in spec §2.4. Unknown fields are rejected.
func ParseRules(body []byte) (Rules, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return Rules{}, fmt.Errorf("rules: decode: %w", err)
	}

	var r Rules
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Rules{}, fmt.Errorf("rules: decode: %w", err)
	}

	// Apply defaults for omitted fields
	if _, ok := m["daily_cap"]; !ok {
		r.DailyCap = DefaultRules().DailyCap
	}
	if _, ok := m["weekly_cap"]; !ok {
		r.WeeklyCap = DefaultRules().WeeklyCap
	}

	if err := r.Validate(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

// MarshalRules serializes a Rules back to canonical JSON.
func MarshalRules(r Rules) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// Validate enforces the constraints documented in spec §2.4 plus the
// two cross-field invariants (autoapply.require_min_score >= min_score;
// autoapply.kinds ⊆ kinds).
func (r Rules) Validate() error {
	if r.Version != 1 {
		return fmt.Errorf("rules: unsupported version %d (only 1)", r.Version)
	}
	if r.MinScore < 0 || r.MinScore > 1 {
		return fmt.Errorf("rules: min_score %v out of [0,1]", r.MinScore)
	}
	if len(r.Kinds) == 0 {
		return errors.New("rules: kinds must be non-empty")
	}
	for _, k := range r.Kinds {
		if !validKinds[k] {
			return fmt.Errorf("rules: invalid kind %q", k)
		}
	}
	if r.DailyCap <= 0 || r.DailyCap > 1000 {
		return fmt.Errorf("rules: daily_cap %d out of (0, 1000]", r.DailyCap)
	}
	if r.WeeklyCap < r.DailyCap || r.WeeklyCap > 5000 {
		return fmt.Errorf("rules: weekly_cap %d out of [daily_cap, 5000]", r.WeeklyCap)
	}
	if r.DismissAfterDays < 0 || r.DismissAfterDays > 365 {
		return fmt.Errorf("rules: dismiss_after_days %d out of [0, 365]", r.DismissAfterDays)
	}
	if r.SalaryFloorUSD < 0 {
		return fmt.Errorf("rules: salary_floor_usd %d must be >= 0", r.SalaryFloorUSD)
	}
	if r.Autoapply.Enabled {
		if r.Autoapply.RequireMinScore < r.MinScore {
			return fmt.Errorf("rules: autoapply.require_min_score %v must be >= min_score %v",
				r.Autoapply.RequireMinScore, r.MinScore)
		}
		if r.Autoapply.RequireMinScore > 1 {
			return fmt.Errorf("rules: autoapply.require_min_score %v out of [0,1]",
				r.Autoapply.RequireMinScore)
		}
		// kinds ⊆ rules.kinds
		allowed := make(map[string]bool, len(r.Kinds))
		for _, k := range r.Kinds {
			allowed[k] = true
		}
		for _, k := range r.Autoapply.Kinds {
			if !allowed[k] {
				return fmt.Errorf("rules: autoapply.kinds[%q] is not a subset of kinds", k)
			}
		}
	}
	return nil
}
