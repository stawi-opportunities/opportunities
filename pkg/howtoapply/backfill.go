// Package howtoapply peels paywalled application instructions from
// opportunity descriptions via the shared extraction LLM (NVIDIA Build in
// production: integrate.api.nvidia.com / meta/llama-3.1-8b-instruct).
package howtoapply

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/stawi-opportunities/opportunities/pkg/extraction"
)

// Peeler is satisfied by *extraction.Extractor (NVIDIA Build chat path).
type Peeler interface {
	PeelHowToApply(ctx context.Context, title, kind, description string) (cleanDescription, howToApply string, err error)
}

// Row is one opportunities row candidate for peeling.
type Row struct {
	CanonicalID string
	Slug        string
	Kind        string
	Title       string
	Description string
	HowToApply  string
	Attrs       []byte
}

// Result counts for one backfill batch.
type Result struct {
	Scanned int `json:"scanned"`
	Peeled  int `json:"peeled"`
	Skipped int `json:"skipped"` // no instructions found / too short
	Failed  int `json:"failed"`
}

// BackfillOptions controls a single admin/CLI batch.
type BackfillOptions struct {
	Limit    int  // max rows to process (default 25)
	Force    bool // recombine + re-peel rows that already have how_to_apply
	MinRunes int  // skip shorter descriptions (default 80)
	DryRun   bool
}

// EnsureColumn adds how_to_apply if missing (idempotent).
func EnsureColumn(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS how_to_apply text`)
	return err
}

// FetchBatch loads the next work set. Non-force only returns unpeeld rows.
func FetchBatch(ctx context.Context, db *sql.DB, limit int, force bool, minRunes int) ([]Row, error) {
	if limit <= 0 {
		limit = 25
	}
	if minRunes <= 0 {
		minRunes = 80
	}
	q := `
SELECT canonical_id, slug, kind, COALESCE(title,''),
       COALESCE(NULLIF(btrim(description),''), COALESCE(attributes->>'description','')),
       COALESCE(how_to_apply,''), COALESCE(attributes,'{}')
  FROM opportunities
 WHERE hidden=false AND status='active'
   AND length(COALESCE(NULLIF(btrim(description),''), COALESCE(attributes->>'description',''))) >= $2`
	if !force {
		q += `
   AND (how_to_apply IS NULL OR btrim(how_to_apply)='')
   AND COALESCE(attributes->>'how_to_apply_peel','') NOT IN ('none','ok')`
	}
	q += `
 ORDER BY first_seen_at ASC
 LIMIT $1`
	rows, err := db.QueryContext(ctx, q, limit, minRunes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.CanonicalID, &r.Slug, &r.Kind, &r.Title, &r.Description, &r.HowToApply, &r.Attrs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProcessOne peels a single row and optionally writes the result.
func ProcessOne(ctx context.Context, db *sql.DB, peeler Peeler, r Row, force, dryRun bool, minRunes int) (peeled bool, err error) {
	if peeler == nil {
		return false, fmt.Errorf("howtoapply: peeler is nil (INFERENCE_* / NVIDIA Build not configured)")
	}
	if minRunes <= 0 {
		minRunes = 80
	}
	src := strings.TrimSpace(r.Description)
	if force && strings.TrimSpace(r.HowToApply) != "" {
		src = strings.TrimSpace(r.Description) + "\n\n## How to Apply\n\n" + strings.TrimSpace(r.HowToApply)
	}
	if utf8.RuneCountInString(src) < minRunes {
		return false, nil
	}
	clean, how, err := peeler.PeelHowToApply(ctx, r.Title, r.Kind, src)
	if err != nil {
		return false, err
	}
	how = strings.TrimSpace(how)
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return false, nil
	}
	if dryRun {
		return how != "", nil
	}
	if how == "" {
		return false, writeMarkNone(ctx, db, r.CanonicalID, clean)
	}
	return true, writeSplit(ctx, db, r.CanonicalID, clean, how, r.Attrs)
}

// Run processes up to opts.Limit rows.
func Run(ctx context.Context, db *sql.DB, peeler Peeler, opts BackfillOptions) (Result, error) {
	var res Result
	if err := EnsureColumn(ctx, db); err != nil {
		return res, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 25
	}
	if opts.MinRunes <= 0 {
		opts.MinRunes = 80
	}
	batch, err := FetchBatch(ctx, db, opts.Limit, opts.Force, opts.MinRunes)
	if err != nil {
		return res, err
	}
	for _, r := range batch {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		res.Scanned++
		did, err := ProcessOne(ctx, db, peeler, r, opts.Force, opts.DryRun, opts.MinRunes)
		if err != nil {
			res.Failed++
			continue
		}
		if did {
			res.Peeled++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

func writeMarkNone(ctx context.Context, db *sql.DB, id, cleanDesc string) error {
	_, err := db.ExecContext(ctx, `
UPDATE opportunities SET
  description = NULLIF($2,''),
  attributes = (COALESCE(attributes,'{}'::jsonb) - 'how_to_apply')
    || jsonb_build_object('description', to_jsonb($2::text),
                          'how_to_apply_peel', 'none'),
  updated_at = now()
 WHERE canonical_id = $1`, id, cleanDesc)
	return err
}

func writeSplit(ctx context.Context, db *sql.DB, id, clean, how string, attrsRaw []byte) error {
	attrs := map[string]any{}
	if len(attrsRaw) > 0 {
		_ = json.Unmarshal(attrsRaw, &attrs)
	}
	delete(attrs, "how_to_apply")
	attrs["description"] = clean
	attrs["how_to_apply_peel"] = "ok"
	raw, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
UPDATE opportunities SET
  description = NULLIF($2,''),
  how_to_apply = NULLIF($3,''),
  attributes = $4::jsonb,
  updated_at = now()
 WHERE canonical_id = $1`, id, clean, how, string(raw))
	return err
}

// Compile-time check: *extraction.Extractor implements Peeler.
var _ Peeler = (*extraction.Extractor)(nil)
