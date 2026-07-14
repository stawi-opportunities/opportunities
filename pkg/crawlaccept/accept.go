// Package crawlaccept is the single place a crawled ExternalOpportunity
// becomes either a durable ingest payload or a structured rejection.
//
// Both apps/crawler (source-level extract) and apps/frontier-worker call
// Accept so "what counts as a storable job" cannot drift between paths.
package crawlaccept

import (
	"maps"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/normalize"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// Input is one extracted record plus the source context needed to gate it.
type Input struct {
	Opp    domain.ExternalOpportunity
	Source *domain.Source
	// Kinds is the opportunity registry (nil → skip Verify; not for prod).
	Kinds *opportunity.Registry
	// Normalizer is optional; when nil, ExternalToVariant is used.
	Normalizer *normalize.Normalizer
	// Now overrides the scrape timestamp (tests).
	Now func() time.Time
}

// Result is either an accepted ingest envelope payload or a rejection.
type Result struct {
	// Accepted is non-nil when the record may be enqueued.
	Accepted *eventsv1.VariantIngestedV1
	// Rejected is set when Verify failed. Callers write audit rows.
	Rejected *Reject
}

// Reject carries structured rejection info for telemetry + ledgers.
type Reject struct {
	Kind    string
	Title   string
	Reason  string // low-cardinality: mismatch | missing_<field> | unknown
	Missing []string
	Detail  string // human-readable mismatch or missing list
}

// Accept prepares, verifies, normalizes, and packs one opportunity.
// It never talks to the network or the queue — pure decision + shape.
func Accept(in Input) Result {
	if in.Source == nil {
		return Result{Rejected: &Reject{Reason: "unknown", Detail: "source required"}}
	}
	opp := in.Opp
	Prepare(&opp, in.Source)

	kind := opp.Kind
	if in.Kinds != nil {
		if res := opportunity.Verify(&opp, in.Source, in.Kinds); !res.OK {
			return Result{Rejected: &Reject{
				Kind:    kind,
				Title:   opp.Title,
				Reason:  RejectionReason(res),
				Missing: res.Missing,
				Detail:  rejectDetail(res),
			}}
		}
	}

	now := time.Now().UTC()
	if in.Now != nil {
		now = in.Now().UTC()
	}

	// Country fallback for normalize: extracted anchor wins (Prepare
	// already filled blanks from source); pass source country as the
	// final fallback parameter.
	srcCountry := strings.TrimSpace(in.Source.Country)
	var variant normalize.JobVariant
	if in.Normalizer != nil {
		variant = in.Normalizer.Normalize(
			&opp, in.Source.ID, srcCountry, string(in.Source.Type), in.Source.Language, now,
		)
	} else {
		variant = normalize.ExternalToVariant(
			opp, in.Source.ID, srcCountry, string(in.Source.Type), in.Source.Language, now,
		)
	}
	if kind == "" {
		kind = "job"
	}

	attrs := maps.Clone(opp.Attributes)
	if attrs == nil {
		attrs = map[string]any{}
	}
	attrs["description"] = variant.Description
	// Never put how_to_apply into Attributes — attributes are returned on the
	// public jobs API. The paywalled body rides on the envelope field only.
	delete(attrs, "how_to_apply")
	attrs["language"] = variant.Language
	attrs["remote_type"] = variant.RemoteType
	attrs["employment_type"] = variant.EmploymentType
	attrs["location_text"] = variant.LocationText
	attrs["content_hash"] = variant.ContentHash
	if len(opp.Categories) > 0 {
		// Persist human category labels so the public facet/filter path
		// can query attributes->'categories' without a separate table.
		cats := make([]string, 0, len(opp.Categories))
		for _, c := range opp.Categories {
			if s := strings.TrimSpace(c); s != "" {
				cats = append(cats, s)
			}
		}
		if len(cats) > 0 {
			attrs["categories"] = cats
		}
	}
	if variant.Region != "" {
		attrs["region"] = variant.Region
	}
	if variant.City != "" {
		attrs["city"] = variant.City
	}
	if variant.PostedAt != nil {
		attrs["posted_at"] = variant.PostedAt.Format(time.RFC3339)
	}
	if variant.Deadline != "" {
		attrs["deadline"] = variant.Deadline
	}
	if variant.Seniority != "" {
		attrs["seniority"] = variant.Seniority
	}

	payload := &eventsv1.VariantIngestedV1{
		VariantID:     xid.New().String(),
		SourceID:      in.Source.ID,
		ExternalID:    variant.ExternalID,
		HardKey:       variant.HardKey,
		Kind:          kind,
		Stage:         string(domain.StageRaw),
		Title:         variant.Title,
		ApplyURL:      variant.ApplyURL,
		HowToApply:    variant.HowToApply,
		IssuingEntity: variant.Company,
		AnchorCountry: variant.Country,
		AnchorRegion:  variant.Region,
		AnchorCity:    variant.City,
		Remote:        variant.RemoteType == "remote",
		Currency:      variant.Currency,
		AmountMin:     variant.SalaryMin,
		AmountMax:     variant.SalaryMax,
		Attributes:    attrs,
		ScrapedAt:     now,
	}
	return Result{Accepted: payload}
}

// Prepare mutates opp in place with the standard pre-Verify fallbacks
// every path must apply so Verify sees complete records.
func Prepare(opp *domain.ExternalOpportunity, src *domain.Source) {
	if opp == nil || src == nil {
		return
	}
	// Apply URL chain: explicit → source_url → never invent base_url here
	// (base_url as apply_url is almost always wrong for multi-job boards).
	if strings.TrimSpace(opp.ApplyURL) == "" {
		opp.ApplyURL = strings.TrimSpace(opp.SourceURL)
	}
	// Kind: connector tag, else source's first declared kind.
	if strings.TrimSpace(opp.Kind) == "" && len(src.Kinds) > 0 {
		opp.Kind = src.Kinds[0]
	}
	if strings.TrimSpace(opp.Kind) == "" {
		opp.Kind = "job"
	}
	// Country: extracted city/country preferred; fill blanks from source.
	if src.Country != "" {
		if opp.AnchorLocation == nil {
			opp.AnchorLocation = &domain.Location{Country: src.Country}
		} else if strings.TrimSpace(opp.AnchorLocation.Country) == "" {
			opp.AnchorLocation.Country = src.Country
		}
	}
	// Location text from structured location when free-text blank.
	if strings.TrimSpace(opp.LocationText) == "" && opp.AnchorLocation != nil {
		parts := make([]string, 0, 3)
		for _, p := range []string{opp.AnchorLocation.City, opp.AnchorLocation.Region, opp.AnchorLocation.Country} {
			if s := strings.TrimSpace(p); s != "" {
				parts = append(parts, s)
			}
		}
		opp.LocationText = strings.Join(parts, ", ")
	}
}

// RejectionReason categorises a VerifyResult into a low-cardinality label.
func RejectionReason(r opportunity.VerifyResult) string {
	if r.Mismatch != "" {
		return "mismatch"
	}
	if len(r.Missing) > 0 {
		return "missing_" + r.Missing[0]
	}
	return "unknown"
}

func rejectDetail(r opportunity.VerifyResult) string {
	if r.Mismatch != "" {
		return r.Mismatch
	}
	if len(r.Missing) > 0 {
		return "missing: " + strings.Join(r.Missing, ",")
	}
	return "rejected"
}
