// apps/api/cmd/jobs_admin.go
//
// Admin surface for browsing and sanitizing crawled opportunities.
// Public reads remain on /api/jobs/* (active, non-hidden only). Operators
// use this surface to:
//
//	GET    /admin/opportunities[?q=&kind=&country=&source_id=&hidden=&limit=&offset=]
//	GET    /admin/opportunities/{slug}
//	PATCH  /admin/opportunities/{slug}   — clear fields / attribute keys / set values
//	DELETE /admin/opportunities/{slug}   — soft-hide (hidden=true)
//	POST   /admin/opportunities/{slug}/unhide
//	GET    /admin/ops/overview           — crawl pipeline at-a-glance
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

type jobsAdmin struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

func registerJobsAdmin(mux *http.ServeMux, db func(ctx context.Context, readOnly bool) *gorm.DB) {
	a := &jobsAdmin{db: db}
	mux.HandleFunc("GET /admin/ops/overview", requireAdmin(a.handleOpsOverview))
	mux.HandleFunc("GET /admin/opportunities", requireAdmin(a.handleList))
	mux.HandleFunc("GET /admin/opportunities/{slug}", requireAdmin(a.handleGet))
	mux.HandleFunc("PATCH /admin/opportunities/{slug}", requireAdmin(a.handlePatch))
	mux.HandleFunc("DELETE /admin/opportunities/{slug}", requireAdmin(a.handleDelete))
	mux.HandleFunc("POST /admin/opportunities/{slug}/unhide", requireAdmin(a.handleUnhide))
}

// adminOpportunity is the wire shape for operator review (includes hidden).
type adminOpportunity struct {
	CanonicalID    string         `json:"canonical_id"`
	Slug           string         `json:"slug"`
	Kind           string         `json:"kind"`
	SourceID       string         `json:"source_id,omitempty"`
	Title          string         `json:"title"`
	Description    string         `json:"description,omitempty"`
	IssuingEntity  string         `json:"issuing_entity,omitempty"`
	Country        string         `json:"country,omitempty"`
	Region         string         `json:"region,omitempty"`
	City           string         `json:"city,omitempty"`
	Remote         bool           `json:"remote"`
	ApplyURL       string         `json:"apply_url"`
	PostedAt       *time.Time     `json:"posted_at,omitempty"`
	Deadline       *time.Time     `json:"deadline,omitempty"`
	Currency       string         `json:"currency,omitempty"`
	AmountMin      float64        `json:"amount_min,omitempty"`
	AmountMax      float64        `json:"amount_max,omitempty"`
	EmploymentType string         `json:"employment_type,omitempty"`
	Seniority      string         `json:"seniority,omitempty"`
	GeoScope       string         `json:"geo_scope,omitempty"`
	Status         string         `json:"status"`
	Hidden         bool           `json:"hidden"`
	HiddenReason   string         `json:"hidden_reason,omitempty"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	Attributes     map[string]any `json:"attributes,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (a *jobsAdmin) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	where := []string{"1=1"}
	args := []any{}
	if v := strings.TrimSpace(q.Get("kind")); v != "" {
		args = append(args, v)
		where = append(where, "kind = ?")
	}
	if v := strings.ToUpper(strings.TrimSpace(q.Get("country"))); v != "" {
		args = append(args, v)
		where = append(where, "country = ?")
	}
	if v := strings.TrimSpace(q.Get("source_id")); v != "" {
		args = append(args, v)
		where = append(where, "source_id = ?")
	}
	switch strings.ToLower(strings.TrimSpace(q.Get("hidden"))) {
	case "true", "1", "yes":
		where = append(where, "hidden = true")
	case "false", "0", "no":
		where = append(where, "hidden = false")
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		like := "%" + v + "%"
		args = append(args, like, like, like)
		where = append(where, "(title ILIKE ? OR issuing_entity ILIKE ? OR slug ILIKE ?)")
	}

	pred := strings.Join(where, " AND ")
	var total int64
	if err := a.db(r.Context(), true).Raw(
		`SELECT count(*) FROM opportunities WHERE `+pred, args...,
	).Scan(&total).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	listArgs := append(append([]any{}, args...), limit, offset)
	var rows []jobqueue.OpportunityRecord
	if err := a.db(r.Context(), true).Raw(
		`SELECT canonical_id, slug, kind, source_id, title, description, issuing_entity,
		        country, region, city, remote, apply_url, posted_at, deadline, currency,
		        amount_min, amount_max, employment_type, seniority, geo_scope, status,
		        first_seen_at, last_seen_at, attributes, hidden, hidden_reason,
		        created_at, updated_at
		   FROM opportunities
		  WHERE `+pred+`
		  ORDER BY last_seen_at DESC
		  LIMIT ? OFFSET ?`, listArgs...,
	).Scan(&rows).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	out := make([]adminOpportunity, 0, len(rows))
	for i := range rows {
		out = append(out, toAdminOpportunity(&rows[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"opportunities": out,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	})
}

func (a *jobsAdmin) handleGet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	row, err := a.loadBySlug(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "not_found", "opportunity not found")
		return
	}
	writeJSON(w, http.StatusOK, toAdminOpportunity(row))
}

// opportunityPatch is the body for PATCH /admin/opportunities/{slug}.
//
// ClearFields zero-or-nulls scalar columns (description, issuing_entity,
// region, city, currency, amount_min, amount_max, employment_type,
// seniority, geo_scope, deadline). ClearAttributes removes keys from the
// attributes jsonb bag. SetAttributes merges (or overwrites) keys.
// Title / ApplyURL / Kind when non-empty replace the stored value.
// Hide when non-nil forces hidden state (with optional HiddenReason).
type opportunityPatch struct {
	Title           *string        `json:"title,omitempty"`
	Description     *string        `json:"description,omitempty"`
	IssuingEntity   *string        `json:"issuing_entity,omitempty"`
	ApplyURL        *string        `json:"apply_url,omitempty"`
	Country         *string        `json:"country,omitempty"`
	Region          *string        `json:"region,omitempty"`
	City            *string        `json:"city,omitempty"`
	Currency        *string        `json:"currency,omitempty"`
	EmploymentType  *string        `json:"employment_type,omitempty"`
	Seniority       *string        `json:"seniority,omitempty"`
	GeoScope        *string        `json:"geo_scope,omitempty"`
	ClearFields     []string       `json:"clear_fields,omitempty"`
	ClearAttributes []string       `json:"clear_attributes,omitempty"`
	SetAttributes   map[string]any `json:"set_attributes,omitempty"`
	Hide            *bool          `json:"hide,omitempty"`
	HiddenReason    *string        `json:"hidden_reason,omitempty"`
}

var clearableFields = map[string]string{
	"description":     "description = NULL",
	"issuing_entity":  "issuing_entity = NULL",
	"region":          "region = NULL",
	"city":            "city = NULL",
	"currency":        "currency = NULL",
	"amount_min":      "amount_min = NULL",
	"amount_max":      "amount_max = NULL",
	"employment_type": "employment_type = NULL",
	"seniority":       "seniority = NULL",
	"geo_scope":       "geo_scope = NULL",
	"deadline":        "deadline = NULL",
	"posted_at":       "posted_at = NULL",
}

func (a *jobsAdmin) handlePatch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	var req opportunityPatch
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}

	row, err := a.loadBySlug(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "not_found", "opportunity not found")
		return
	}

	sets := []string{"updated_at = now()"}
	args := []any{}

	setStr := func(col string, p *string) {
		if p == nil {
			return
		}
		v := strings.TrimSpace(*p)
		if v == "" {
			return
		}
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	setStr("title", req.Title)
	setStr("description", req.Description)
	setStr("issuing_entity", req.IssuingEntity)
	setStr("country", req.Country)
	setStr("region", req.Region)
	setStr("city", req.City)
	setStr("currency", req.Currency)
	setStr("employment_type", req.EmploymentType)
	setStr("seniority", req.Seniority)
	setStr("geo_scope", req.GeoScope)
	if req.ApplyURL != nil {
		v := strings.TrimSpace(*req.ApplyURL)
		if v == "" {
			writeError(w, http.StatusBadRequest, "invalid_apply_url", "apply_url cannot be empty")
			return
		}
		sets = append(sets, "apply_url = ?")
		args = append(args, v)
	}
	for _, f := range req.ClearFields {
		sqlFrag, ok := clearableFields[strings.ToLower(strings.TrimSpace(f))]
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_clear_field",
				"unknown clear_fields entry: "+f)
			return
		}
		sets = append(sets, sqlFrag)
	}

	// Attribute bag mutations (merge then persist as a whole).
	attrs := map[string]any{}
	if len(row.Attributes) > 0 {
		_ = json.Unmarshal(row.Attributes, &attrs)
	}
	attrsChanged := false
	for _, k := range req.ClearAttributes {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		delete(attrs, k)
		attrsChanged = true
	}
	for k, v := range req.SetAttributes {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v == nil {
			delete(attrs, k)
		} else {
			attrs[k] = v
		}
		attrsChanged = true
	}
	if attrsChanged {
		b, merr := json.Marshal(attrs)
		if merr != nil {
			writeError(w, http.StatusBadRequest, "bad_attributes", merr.Error())
			return
		}
		sets = append(sets, "attributes = ?::jsonb")
		args = append(args, string(b))
	}

	if req.Hide != nil {
		if *req.Hide {
			reason := "operator_sanitize"
			if req.HiddenReason != nil && strings.TrimSpace(*req.HiddenReason) != "" {
				reason = strings.TrimSpace(*req.HiddenReason)
			}
			sets = append(sets, "hidden = true", "hidden_reason = ?")
			args = append(args, reason)
		} else {
			sets = append(sets, "hidden = false", "hidden_reason = NULL")
		}
	}

	if len(sets) == 1 {
		// only updated_at — nothing to do
		writeJSON(w, http.StatusOK, toAdminOpportunity(row))
		return
	}

	args = append(args, slug)
	sql := `UPDATE opportunities SET ` + strings.Join(sets, ", ") + ` WHERE slug = ?`
	res := a.db(r.Context(), false).WithContext(r.Context()).Exec(sql, args...)
	if res.Error != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", res.Error.Error())
		return
	}
	if res.RowsAffected != 1 {
		writeError(w, http.StatusNotFound, "not_found", "opportunity not found")
		return
	}

	logAction(r, "opportunity_patch", slug)
	updated, err := a.loadBySlug(r.Context(), slug)
	if err != nil || updated == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slug": slug})
		return
	}
	writeJSON(w, http.StatusOK, toAdminOpportunity(updated))
}

func (a *jobsAdmin) handleDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if reason == "" {
		reason = "operator_removed"
	}
	res := a.db(r.Context(), false).WithContext(r.Context()).Exec(
		`UPDATE opportunities SET hidden = true, hidden_reason = ?, updated_at = now() WHERE slug = ?`,
		reason, slug,
	)
	if res.Error != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", res.Error.Error())
		return
	}
	if res.RowsAffected != 1 {
		writeError(w, http.StatusNotFound, "not_found", "opportunity not found")
		return
	}
	logAction(r, "opportunity_hide", slug)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slug": slug, "hidden": true, "reason": reason})
}

func (a *jobsAdmin) handleUnhide(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "slug required")
		return
	}
	res := a.db(r.Context(), false).WithContext(r.Context()).Exec(
		`UPDATE opportunities SET hidden = false, hidden_reason = NULL, updated_at = now() WHERE slug = ?`,
		slug,
	)
	if res.Error != nil {
		writeError(w, http.StatusInternalServerError, "unhide_failed", res.Error.Error())
		return
	}
	if res.RowsAffected != 1 {
		writeError(w, http.StatusNotFound, "not_found", "opportunity not found")
		return
	}
	logAction(r, "opportunity_unhide", slug)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slug": slug, "hidden": false})
}

func (a *jobsAdmin) handleOpsOverview(w http.ResponseWriter, r *http.Request) {
	type counts struct {
		ActiveJobs      int64 `json:"active_jobs"`
		HiddenJobs      int64 `json:"hidden_jobs"`
		SourcesActive   int64 `json:"sources_active"`
		SourcesPaused   int64 `json:"sources_paused"`
		SourcesTotal    int64 `json:"sources_total"`
		QueuePending    int64 `json:"queue_pending"`
		QueueProcessing int64 `json:"queue_processing"`
		QueueDead       int64 `json:"queue_dead"`
		Rejected24h     int64 `json:"rejected_24h"`
		Published24h    int64 `json:"published_24h"`
		CrawlJobs24h    int64 `json:"crawl_jobs_24h"`
		CrawlFailed24h  int64 `json:"crawl_failed_24h"`
		ActiveRuns      int64 `json:"active_runs"`
	}
	var c counts
	err := a.db(r.Context(), true).Raw(`
		SELECT
		  (SELECT count(*) FROM opportunities WHERE hidden = false AND status = 'active') AS active_jobs,
		  (SELECT count(*) FROM opportunities WHERE hidden = true) AS hidden_jobs,
		  (SELECT count(*) FROM sources WHERE status = 'active') AS sources_active,
		  (SELECT count(*) FROM sources WHERE status = 'paused') AS sources_paused,
		  (SELECT count(*) FROM sources) AS sources_total,
		  (SELECT count(*) FROM job_ingest_queue WHERE status IN ('pending','retry')) AS queue_pending,
		  (SELECT count(*) FROM job_ingest_queue WHERE status = 'processing') AS queue_processing,
		  (SELECT count(*) FROM job_ingest_queue WHERE status = 'dead') AS queue_dead,
		  (SELECT count(*) FROM job_ingest_events WHERE event_type = 'rejected' AND occurred_at >= now() - interval '24 hours') AS rejected_24h,
		  (SELECT count(*) FROM job_ingest_events WHERE event_type = 'processed' AND occurred_at >= now() - interval '24 hours') AS published_24h,
		  (SELECT count(*) FROM crawl_jobs WHERE scheduled_at >= now() - interval '24 hours') AS crawl_jobs_24h,
		  (SELECT count(*) FROM crawl_jobs WHERE scheduled_at >= now() - interval '24 hours' AND status = 'failed') AS crawl_failed_24h,
		  (SELECT count(*) FROM crawl_runs WHERE status IN ('running','paused')) AS active_runs
	`).Row().Scan(
		&c.ActiveJobs, &c.HiddenJobs,
		&c.SourcesActive, &c.SourcesPaused, &c.SourcesTotal,
		&c.QueuePending, &c.QueueProcessing, &c.QueueDead,
		&c.Rejected24h, &c.Published24h,
		&c.CrawlJobs24h, &c.CrawlFailed24h, &c.ActiveRuns,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "overview_failed", err.Error())
		return
	}

	// Top rejection reasons (24h, global).
	reasons := map[string]int64{}
	rows, rerr := a.db(r.Context(), true).Raw(`
		SELECT COALESCE(NULLIF(reason, ''), 'unknown') AS reason, count(*)::bigint
		  FROM (
		    SELECT jsonb_array_elements_text(
		             CASE WHEN jsonb_typeof(details->'reasons') = 'array'
		                  THEN details->'reasons' ELSE '[]'::jsonb END
		           ) AS reason
		      FROM job_ingest_events
		     WHERE event_type = 'rejected' AND occurred_at >= now() - interval '24 hours'
		    UNION ALL
		    SELECT details->>'reason'
		      FROM job_ingest_events
		     WHERE event_type = 'rejected' AND occurred_at >= now() - interval '24 hours'
		       AND COALESCE(details->>'reason','') <> ''
		       AND (details->'reasons' IS NULL OR jsonb_typeof(details->'reasons') <> 'array'
		            OR jsonb_array_length(details->'reasons') = 0)
		  ) x
		 GROUP BY 1 ORDER BY 2 DESC LIMIT 20
	`).Rows()
	if rerr == nil {
		defer rows.Close()
		for rows.Next() {
			var reason string
			var n int64
			if rows.Scan(&reason, &n) == nil {
				reasons[reason] = n
			}
		}
	}

	// Recent opportunities for the ops dashboard feed.
	var recent []jobqueue.OpportunityRecord
	_ = a.db(r.Context(), true).Raw(`
		SELECT canonical_id, slug, kind, source_id, title, description, issuing_entity,
		       country, region, city, remote, apply_url, posted_at, deadline, currency,
		       amount_min, amount_max, employment_type, seniority, geo_scope, status,
		       first_seen_at, last_seen_at, attributes, hidden, hidden_reason,
		       created_at, updated_at
		  FROM opportunities
		 ORDER BY last_seen_at DESC
		 LIMIT 15
	`).Scan(&recent).Error
	recentOut := make([]adminOpportunity, 0, len(recent))
	for i := range recent {
		recentOut = append(recentOut, toAdminOpportunity(&recent[i]))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"counts":            c,
		"rejection_reasons": reasons,
		"recent":            recentOut,
		"generated_at":      time.Now().UTC(),
	})
}

func (a *jobsAdmin) loadBySlug(ctx context.Context, slug string) (*jobqueue.OpportunityRecord, error) {
	var row jobqueue.OpportunityRecord
	err := a.db(ctx, true).Raw(`
		SELECT canonical_id, slug, kind, source_id, title, description, issuing_entity,
		       country, region, city, remote, apply_url, posted_at, deadline, currency,
		       amount_min, amount_max, employment_type, seniority, geo_scope, status,
		       first_seen_at, last_seen_at, attributes, hidden, hidden_reason,
		       created_at, updated_at
		  FROM opportunities WHERE slug = ? LIMIT 1`, slug).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.Slug == "" {
		return nil, nil
	}
	return &row, nil
}

func toAdminOpportunity(r *jobqueue.OpportunityRecord) adminOpportunity {
	out := adminOpportunity{
		CanonicalID: r.CanonicalID,
		Slug:        r.Slug,
		Kind:        r.Kind,
		Title:       r.Title,
		Remote:      r.Remote,
		ApplyURL:    r.ApplyURL,
		Status:      r.Status,
		Hidden:      r.Hidden,
		FirstSeenAt: r.FirstSeenAt,
		LastSeenAt:  r.LastSeenAt,
		UpdatedAt:   r.UpdatedAt,
		PostedAt:    r.PostedAt,
		Deadline:    r.Deadline,
	}
	if r.SourceID != nil {
		out.SourceID = *r.SourceID
	}
	if r.Description != nil {
		out.Description = *r.Description
	}
	if r.IssuingEntity != nil {
		out.IssuingEntity = *r.IssuingEntity
	}
	if r.Country != nil {
		out.Country = *r.Country
	}
	if r.Region != nil {
		out.Region = *r.Region
	}
	if r.City != nil {
		out.City = *r.City
	}
	if r.Currency != nil {
		out.Currency = *r.Currency
	}
	if r.AmountMin != nil {
		out.AmountMin = *r.AmountMin
	}
	if r.AmountMax != nil {
		out.AmountMax = *r.AmountMax
	}
	if r.EmploymentType != nil {
		out.EmploymentType = *r.EmploymentType
	}
	if r.Seniority != nil {
		out.Seniority = *r.Seniority
	}
	if r.GeoScope != nil {
		out.GeoScope = *r.GeoScope
	}
	if r.HiddenReason != nil {
		out.HiddenReason = *r.HiddenReason
	}
	if len(r.Attributes) > 0 {
		var attrs map[string]any
		if json.Unmarshal(r.Attributes, &attrs) == nil {
			out.Attributes = attrs
		}
	}
	return out
}
