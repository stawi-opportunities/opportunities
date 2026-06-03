// Package variantstate is the worker-side write API for the
// pipeline_variants Postgres table — the master ledger that replaces
// "purge the NATS stream to recover" with "query the database."
//
// Design notes:
//
//   - Every method is best-effort: a Postgres outage degrades
//     observability but does not stall the chain. Errors are logged
//     as WARN and returned as nil to the caller. This mirrors the
//     soft-fail pattern in worker dedup/canonical (v8.0.32+) — under
//     the worst case (Postgres down), the pipeline keeps moving on
//     the strength of NATS + R2.
//
//   - AdvanceStage uses a CAS pattern (`WHERE current_stage = $from`)
//     so a redelivered NATS message can't double-advance the row.
//     If the row is already past $from, the UPDATE no-ops and we
//     return nil without error.
//
//   - The package does not enforce stage ordering — that lives in
//     the worker chain handlers themselves. variantstate just records
//     transitions it's told about.
package variantstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/pitabwire/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// strconvAppendFloat32 wraps strconv.AppendFloat with the right bitSize
// for vectorLiteral. Defined as a function so its non-trivial signature
// stays out of the call site in store.go's hot path.
func strconvAppendFloat32(b []byte, f float32) []byte {
	return strconv.AppendFloat(b, float64(f), 'f', -1, 32)
}

// jsonMarshal returns the JSON encoding of v as a string, or "" when v
// is nil or marshalling fails. The empty-string output drives the SQL
// COALESCE fallback to '{}'::jsonb in UpsertOpportunity, so a nil
// Attributes map merges with the existing row unchanged.
func jsonMarshal(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(b) == "null" {
		return "", nil
	}
	return string(b), nil
}

// nullStr converts a string-pointer to an `any` suitable for GORM Exec
// arg binding. nil pointer or empty-string deref both yield nil so the
// COALESCE/NULLIF combo in UpsertOpportunity preserves the prior value.
func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	if *p == "" {
		return nil
	}
	return *p
}

// Stage values match the migration's expected current_stage strings.
const (
	StageIngested    = "ingested"
	StageNormalized  = "normalized"
	StageValidated   = "validated"
	StageClustered   = "clustered"
	StageCanonical   = "canonical"
	StagePublished   = "published"
	StageManticore   = "manticore" // legacy; remove once Manticore decommissioned
	StageFlagged     = "flagged"
	StageRejected    = "rejected"
	// StageEmbed is a side-channel stage label used only by the embed
	// queue worker's attempts ledger. It is NOT a current_stage value —
	// embedding runs off the canonical fan-out and never advances
	// current_stage — it just namespaces the attempts/last_error columns
	// so a stuck embed is visible and boundable.
	StageEmbed = "embed"
	// StageTranslate is the analogous side-channel label for the translate
	// queue worker's attempts ledger; not a current_stage value.
	StageTranslate = "translate"
)

// Variant maps to the pipeline_variants row.
//
// pipeline_variants is a TimescaleDB hypertable partitioned by
// ingested_at; its primary key is the composite (variant_id,
// ingested_at) because hypertables require the partition column in
// every unique constraint. variant_id is xid-generated so the
// composite tuple is unique in practice — the second column is purely
// structural for TimescaleDB's chunk-pruning machinery.
//
// GORM column names follow the migration's CREATE TABLE exactly so
// AutoMigrate is unnecessary (the SQL Job owns the schema; this
// struct is read-write only).
type Variant struct {
	VariantID       string         `gorm:"primaryKey;column:variant_id"`
	IngestedAt      time.Time      `gorm:"primaryKey;column:ingested_at;not null;default:now()"`
	SourceID        string         `gorm:"column:source_id;not null"`
	HardKey         string         `gorm:"column:hard_key;not null"`
	Kind            string         `gorm:"column:kind;not null"`
	CurrentStage    string         `gorm:"column:current_stage;not null;default:ingested"`
	CanonicalID     *string        `gorm:"column:canonical_id"`
	Slug            *string        `gorm:"column:slug"`
	RawContentHash  *string        `gorm:"column:raw_content_hash"`
	// RawPayloadID + CrawlJobID forward-link a variant row to the
	// audit ledger written by the crawler (raw_payloads / crawl_jobs).
	// Both nullable because (a) the rejected-variant path writes a row
	// before either link is meaningful, and (b) tests construct
	// variants without a backing crawl.
	RawPayloadID    *string        `gorm:"column:raw_payload_id"`
	CrawlJobID      *string        `gorm:"column:crawl_job_id"`
	StageAt         time.Time      `gorm:"column:stage_at;not null;default:now()"`
	Attempts        map[string]any `gorm:"column:attempts;type:jsonb;serializer:json"`
	LastError       *string        `gorm:"column:last_error"`
	CreatedAt       time.Time      `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt       time.Time      `gorm:"column:updated_at;not null;default:now()"`
}

// TableName tells GORM the exact table name (avoids snake-case rules
// adding a "s" that doesn't match the schema).
func (Variant) TableName() string { return "pipeline_variants" }

// Store is the write API the worker uses. db is the same function
// shape the rest of the codebase uses (frame.DatastoreManager().Pool…DB).
type Store struct {
	db func(ctx context.Context, readOnly bool) *gorm.DB
}

// NewStore wires the store. Pass the read/write DB factory from the
// Frame service.
func NewStore(db func(ctx context.Context, readOnly bool) *gorm.DB) *Store {
	return &Store{db: db}
}

// Upsert creates the pipeline_variants row for a freshly-emitted
// variants.ingested. Idempotent on variant_id (PK) so a redelivery
// is a no-op.
//
// Called by:
//   - the crawler, immediately after the variants.ingested NATS emit
//   - the worker's NormalizeHandler as a defensive backstop (handles
//     the case where the worker sees variants.ingested before the
//     crawler's row commit lands due to Postgres replication lag)
func (s *Store) Upsert(ctx context.Context, v Variant) error {
	if s == nil || s.db == nil {
		return nil // no-op when wiring is absent (test paths)
	}
	if v.StageAt.IsZero() {
		v.StageAt = time.Now().UTC()
	}
	if v.IngestedAt.IsZero() {
		v.IngestedAt = v.StageAt
	}
	if v.CurrentStage == "" {
		v.CurrentStage = StageIngested
	}
	// pipeline_variants.attempts is NOT NULL DEFAULT '{}'::jsonb. GORM's
	// json serializer writes NULL for a nil map, violating the column
	// constraint. Default to an empty map at the Go layer so the INSERT
	// always carries a valid JSON object.
	if v.Attempts == nil {
		v.Attempts = map[string]any{}
	}
	// Composite PK target — TimescaleDB requires the partition column
	// (ingested_at) in every unique constraint. variant_id is
	// xid-generated so the tuple is unique in practice; DO NOTHING
	// guards against the rare same-tuple replay.
	err := s.db(ctx, false).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "variant_id"}, {Name: "ingested_at"}},
			DoNothing: true,
		}).
		Create(&v).Error
	return s.soft(ctx, err, "upsert", v.VariantID, "")
}

// AdvanceStage transitions a variant from one stage to the next
// atomically. The CAS guard prevents double-advance under NATS
// redelivery.
//
// Set canonicalID/slug to non-nil to attach those identifiers as
// the row reaches the canonical / published stages. Pass nil pointers
// when not applicable (e.g. moving from ingested → normalized).
func (s *Store) AdvanceStage(
	ctx context.Context,
	variantID, fromStage, toStage string,
	canonicalID, slug *string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	updates := map[string]any{
		"current_stage": toStage,
		"stage_at":      time.Now().UTC(),
	}
	if canonicalID != nil {
		updates["canonical_id"] = *canonicalID
	}
	if slug != nil {
		updates["slug"] = *slug
	}

	tx := s.db(ctx, false).
		Table("pipeline_variants").
		Where("variant_id = ?", variantID).
		Where("current_stage = ?", fromStage).
		Updates(updates)
	if tx.Error != nil {
		return s.soft(ctx, tx.Error, "advance", variantID, toStage)
	}
	if tx.RowsAffected == 0 {
		// Either the row is missing (crawler write hasn't landed yet),
		// or it's already past this stage (redelivery). Both are safe
		// to silently accept — the chain doesn't need DB confirmation
		// to proceed.
		util.Log(ctx).
			WithField("variant_id", variantID).
			WithField("from_stage", fromStage).
			WithField("to_stage", toStage).
			Debug("variantstate: advance no-op (row missing or already past stage)")
	}
	return nil
}

// LookupCanonical returns the cluster_id assigned to the first
// variant that recorded this hard_key, or nil if no prior assignment
// exists. Used by DedupHandler to replace the Valkey hard_key →
// cluster_id lookup.
//
// The query hits the partial index pipeline_variants_hard_key_canonical_idx
// so it's a fast point lookup. On Postgres error we return (nil, nil)
// to let dedup treat it as a miss — same soft-fail philosophy.
func (s *Store) LookupCanonical(ctx context.Context, hardKey string) (*string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var row struct {
		CanonicalID *string
	}
	err := s.db(ctx, true).
		Table("pipeline_variants").
		Select("canonical_id").
		Where("hard_key = ? AND canonical_id IS NOT NULL", hardKey).
		Limit(1).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		util.Log(ctx).WithError(err).
			WithField("hard_key", hardKey).
			Warn("variantstate: hard_key lookup failed; treating as miss")
		return nil, nil
	}
	return row.CanonicalID, nil
}

// MarkPublishedByCanonical advances every variant in a canonical
// cluster from `canonical` to `published` in a single statement.
// Used by PublishHandler, which consumes CanonicalUpsertedV1 — the
// many-to-one fan-in point. The event does not carry the individual
// variant_id, so we update the whole cluster atomically.
//
// The CAS guard (`current_stage = 'canonical'`) keeps the update
// idempotent: republishing the same canonical doesn't backfill rows
// stuck at earlier stages.
func (s *Store) MarkPublishedByCanonical(ctx context.Context, canonicalID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db(ctx, false).
		Table("pipeline_variants").
		Where("canonical_id = ?", canonicalID).
		Where("current_stage = ?", StageCanonical).
		Updates(map[string]any{
			"current_stage": StagePublished,
			"stage_at":      time.Now().UTC(),
		}).Error
	return s.soft(ctx, err, "mark_published", "", canonicalID)
}

// RecordError attaches an error to the row + bumps the attempts
// counter for this stage. Used by handlers that want to surface
// transient failures without changing current_stage (so the next
// redelivery retries the same stage).
func (s *Store) RecordError(ctx context.Context, variantID, stage string, e error) error {
	if s == nil || s.db == nil || e == nil {
		return nil
	}
	msg := e.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	// jsonb_set(attempts, '{stage}', (COALESCE(attempts->>'stage','0')::int + 1)::text::jsonb, true)
	err := s.db(ctx, false).
		Exec(`
            UPDATE pipeline_variants
            SET    last_error = ?,
                   attempts   = jsonb_set(
                       attempts,
                       ARRAY[?]::text[],
                       to_jsonb((COALESCE(NULLIF(attempts->>?, '')::int, 0) + 1)),
                       true),
                   updated_at = now()
            WHERE  variant_id = ?
        `, msg, stage, stage, variantID).Error
	return s.soft(ctx, err, "record_error", variantID, stage)
}

// RecordErrorByCanonical records an error against every variant in a
// canonical cluster, bumping each row's per-stage attempts counter.
// Used by PublishHandler, which consumes the many-to-one CanonicalUpsertedV1
// fan-in and therefore has no single variant_id to key on. Same soft-fail
// + jsonb attempts-increment semantics as RecordError.
func (s *Store) RecordErrorByCanonical(ctx context.Context, canonicalID, stage string, e error) error {
	if s == nil || s.db == nil || e == nil || canonicalID == "" {
		return nil
	}
	msg := e.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	err := s.db(ctx, false).
		Exec(`
            UPDATE pipeline_variants
            SET    last_error = ?,
                   attempts   = jsonb_set(
                       attempts,
                       ARRAY[?]::text[],
                       to_jsonb((COALESCE(NULLIF(attempts->>?, '')::int, 0) + 1)),
                       true),
                   updated_at = now()
            WHERE  canonical_id = ?
        `, msg, stage, stage, canonicalID).Error
	return s.soft(ctx, err, "record_error_by_canonical", "", canonicalID)
}

// AttemptCount returns the recorded number of attempts for a given
// (variant, stage) pair from the attempts jsonb map, or 0 when the row
// or key is absent. Used by handlers that bound redelivery (e.g. embed)
// so an external-I/O failure can't redeliver forever on a stream with no
// max-deliver cap. Soft-fails to 0 on Postgres error so a DB outage never
// forces a premature drop.
func (s *Store) AttemptCount(ctx context.Context, variantID, stage string) (int, error) {
	if s == nil || s.db == nil || variantID == "" {
		return 0, nil
	}
	var row struct {
		N int
	}
	err := s.db(ctx, true).
		Table("pipeline_variants").
		Select("COALESCE(NULLIF(attempts->>?, '')::int, 0) AS n", stage).
		Where("variant_id = ?", variantID).
		Limit(1).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		util.Log(ctx).WithError(err).
			WithField("variant_id", variantID).
			WithField("stage", stage).
			Warn("variantstate: AttemptCount failed; treating as 0")
		return 0, nil
	}
	return row.N, nil
}

// AttemptCountByCanonical returns the MAX recorded attempts for a stage
// across every variant in a canonical cluster, or 0 when the cluster or
// key is absent. Canonical-keyed sibling of AttemptCount for the embed
// stage, which consumes the many-to-one CanonicalUpsertedV1 fan-in and so
// has no single variant_id. Soft-fails to 0 on Postgres error.
func (s *Store) AttemptCountByCanonical(ctx context.Context, canonicalID, stage string) (int, error) {
	if s == nil || s.db == nil || canonicalID == "" {
		return 0, nil
	}
	var row struct {
		N int
	}
	err := s.db(ctx, true).
		Table("pipeline_variants").
		Select("COALESCE(MAX(COALESCE(NULLIF(attempts->>?, '')::int, 0)), 0) AS n", stage).
		Where("canonical_id = ?", canonicalID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		util.Log(ctx).WithError(err).
			WithField("canonical_id", canonicalID).
			WithField("stage", stage).
			Warn("variantstate: AttemptCountByCanonical failed; treating as 0")
		return 0, nil
	}
	return row.N, nil
}

// ListStuck returns variants whose stage_at is older than the cutoff and
// whose current_stage is not in excludeStages — i.e. rows wedged mid-chain
// (a dropped NATS redelivery, a failed emit, an R2 outage). The reaper
// uses this to re-drive the appropriate stage event. Bounded by limit and
// ordered oldest-first so the most-stuck rows recover first.
//
// Read-only and soft-failing: a Postgres error returns (nil, nil) so a
// sweep is skipped rather than crashing the reaper goroutine.
func (s *Store) ListStuck(ctx context.Context, olderThan time.Duration, excludeStages []string, limit int) ([]Variant, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	q := s.db(ctx, true).
		Table("pipeline_variants").
		Where("stage_at < ?", cutoff)
	if len(excludeStages) > 0 {
		q = q.Where("current_stage NOT IN ?", excludeStages)
	}
	var rows []Variant
	err := q.Order("stage_at ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		util.Log(ctx).WithError(err).
			Warn("variantstate: ListStuck failed; skipping sweep")
		return nil, nil
	}
	return rows, nil
}

func (s *Store) soft(ctx context.Context, err error, op, variantID, stage string) error {
	if err == nil {
		return nil
	}
	util.Log(ctx).WithError(err).
		WithField("op", op).
		WithField("variant_id", variantID).
		WithField("stage", stage).
		Warn("variantstate: write failed (soft-fail)")
	return nil // soft-fail
}

// Opportunity maps to the opportunities table row — the canonical
// merged state + search/serve record. Worker's CanonicalHandler
// UPSERTs into this table; the materializer extends the same row
// with embedding / hidden flags in Phase 5.
//
// Note: embedding is intentionally not modelled here — pgvector's
// `vector(1024)` type (intfloat/multilingual-e5-large; see EMBEDDING_DIM
// and VerifyEmbeddingDim) doesn't roundtrip cleanly through GORM's
// default driver. UpdateEmbedding uses a raw Exec instead; for the
// canonical UPSERT we don't touch the column.
type Opportunity struct {
	CanonicalID   string         `gorm:"primaryKey;column:canonical_id"`
	Slug          string         `gorm:"column:slug;not null"`
	Kind          string         `gorm:"column:kind;not null"`
	SourceID      *string        `gorm:"column:source_id"`
	Title         string         `gorm:"column:title;not null"`
	Description   *string        `gorm:"column:description"`
	IssuingEntity *string        `gorm:"column:issuing_entity"`
	Country       *string        `gorm:"column:country"`
	Region        *string        `gorm:"column:region"`
	City          *string        `gorm:"column:city"`
	Remote        *bool          `gorm:"column:remote"`
	ApplyURL      *string        `gorm:"column:apply_url"`
	PostedAt      *time.Time     `gorm:"column:posted_at"`
	Deadline      *time.Time     `gorm:"column:deadline"`
	Currency      *string        `gorm:"column:currency"`
	AmountMin     *float64       `gorm:"column:amount_min"`
	AmountMax     *float64       `gorm:"column:amount_max"`
	EmploymentType *string       `gorm:"column:employment_type"`
	Seniority      *string       `gorm:"column:seniority"`
	GeoScope       *string       `gorm:"column:geo_scope"`
	Status        string         `gorm:"column:status;not null;default:active"`
	FirstSeenAt   time.Time      `gorm:"column:first_seen_at;not null;default:now()"`
	LastSeenAt    time.Time      `gorm:"column:last_seen_at;not null;default:now()"`
	Attributes    map[string]any `gorm:"column:attributes;type:jsonb;serializer:json"`
	QualityScore  *float64       `gorm:"column:quality_score"`
	Hidden        bool           `gorm:"column:hidden;not null;default:false"`
	HiddenReason  *string        `gorm:"column:hidden_reason"`
}

// TableName binds GORM to the table name (the package is variantstate
// but the table is opportunities — keep them decoupled).
func (Opportunity) TableName() string { return "opportunities" }

// UpsertOpportunity writes the canonical merged state.
//
// Conflict semantics:
//   - canonical_id is the PK; ON CONFLICT updates the row in place.
//   - last_seen_at always advances; first_seen_at sticks at the
//     pre-existing value.
//   - String/numeric fields use COALESCE(NULLIF(EXCLUDED, ''), opp)
//     so a sparse subsequent variant can't blank an established field.
//     Same merge philosophy as the in-memory mergeAttributes in the
//     CanonicalHandler — newer data wins when non-empty, otherwise the
//     prior value survives.
//   - attributes JSONB merges with `||` (prior keys preserved,
//     incoming keys overwrite). Matches the per-variant Attribute
//     accumulation that drives downstream per-kind facets.
//
// Soft-fail per the package contract: Postgres outage logs WARN and
// returns nil so the canonical chain proceeds.
func (s *Store) UpsertOpportunity(ctx context.Context, o Opportunity) error {
	if s == nil || s.db == nil {
		return nil
	}
	if o.CanonicalID == "" || o.Slug == "" || o.Title == "" || o.Kind == "" {
		// Phase 4 invariant: canonical UPSERT requires the four NOT NULL
		// columns. A missing one is a worker bug; log + drop the write.
		util.Log(ctx).
			WithField("canonical_id", o.CanonicalID).
			WithField("slug", o.Slug).
			Warn("variantstate: UpsertOpportunity skipped — required field empty")
		return nil
	}
	now := time.Now().UTC()
	if o.LastSeenAt.IsZero() {
		o.LastSeenAt = now
	}
	if o.FirstSeenAt.IsZero() {
		o.FirstSeenAt = now
	}
	attrsJSON, _ := jsonMarshal(o.Attributes)

	err := s.db(ctx, false).
		Exec(`
            INSERT INTO opportunities (
                canonical_id, slug, kind, source_id,
                title, description, issuing_entity,
                country, region, city,
                remote, apply_url, posted_at, deadline,
                currency, amount_min, amount_max,
                employment_type, seniority, geo_scope,
                status, first_seen_at, last_seen_at,
                attributes, quality_score, hidden, hidden_reason
            ) VALUES (
                ?, ?, ?, ?,
                ?, ?, ?,
                ?, ?, ?,
                ?, ?, ?, ?,
                ?, ?, ?,
                ?, ?, ?,
                ?, ?, ?,
                COALESCE(?::jsonb, '{}'::jsonb), ?, ?, ?
            )
            ON CONFLICT (canonical_id) DO UPDATE SET
                slug            = COALESCE(NULLIF(EXCLUDED.slug,''),            opportunities.slug),
                kind            = COALESCE(NULLIF(EXCLUDED.kind,''),            opportunities.kind),
                source_id       = COALESCE(NULLIF(EXCLUDED.source_id,''),       opportunities.source_id),
                title           = COALESCE(NULLIF(EXCLUDED.title,''),           opportunities.title),
                description     = COALESCE(NULLIF(EXCLUDED.description,''),     opportunities.description),
                issuing_entity  = COALESCE(NULLIF(EXCLUDED.issuing_entity,''),  opportunities.issuing_entity),
                country         = COALESCE(NULLIF(EXCLUDED.country,''),         opportunities.country),
                region          = COALESCE(NULLIF(EXCLUDED.region,''),          opportunities.region),
                city            = COALESCE(NULLIF(EXCLUDED.city,''),            opportunities.city),
                remote          = COALESCE(EXCLUDED.remote,                     opportunities.remote),
                apply_url       = COALESCE(NULLIF(EXCLUDED.apply_url,''),       opportunities.apply_url),
                posted_at       = COALESCE(EXCLUDED.posted_at,                  opportunities.posted_at),
                deadline        = COALESCE(EXCLUDED.deadline,                   opportunities.deadline),
                currency        = COALESCE(NULLIF(EXCLUDED.currency,''),        opportunities.currency),
                amount_min      = GREATEST(COALESCE(EXCLUDED.amount_min, 0),    COALESCE(opportunities.amount_min, 0)),
                amount_max      = GREATEST(COALESCE(EXCLUDED.amount_max, 0),    COALESCE(opportunities.amount_max, 0)),
                employment_type = COALESCE(NULLIF(EXCLUDED.employment_type,''), opportunities.employment_type),
                seniority       = COALESCE(NULLIF(EXCLUDED.seniority,''),       opportunities.seniority),
                geo_scope       = COALESCE(NULLIF(EXCLUDED.geo_scope,''),       opportunities.geo_scope),
                status          = COALESCE(NULLIF(EXCLUDED.status,''),          opportunities.status),
                last_seen_at    = GREATEST(EXCLUDED.last_seen_at,               opportunities.last_seen_at),
                attributes      = opportunities.attributes || COALESCE(EXCLUDED.attributes, '{}'::jsonb),
                quality_score   = COALESCE(EXCLUDED.quality_score,              opportunities.quality_score),
                updated_at      = now()
        `,
			o.CanonicalID, o.Slug, o.Kind, nullStr(o.SourceID),
			o.Title, nullStr(o.Description), nullStr(o.IssuingEntity),
			nullStr(o.Country), nullStr(o.Region), nullStr(o.City),
			o.Remote, nullStr(o.ApplyURL), o.PostedAt, o.Deadline,
			nullStr(o.Currency), o.AmountMin, o.AmountMax,
			nullStr(o.EmploymentType), nullStr(o.Seniority), nullStr(o.GeoScope),
			o.Status, o.FirstSeenAt, o.LastSeenAt,
			attrsJSON, o.QualityScore, o.Hidden, nullStr(o.HiddenReason),
		).Error
	return s.soft(ctx, err, "upsert_opportunity", "", o.CanonicalID)
}

// EmbeddingDim returns the declared dimension of opportunities.embedding
// (the vector(N) typmod), or 0 when the table/column is absent (fresh
// environment or test stub) OR declared without a fixed dimension (plain
// `vector`, atttypmod -1). Unlike varchar (whose atttypmod is length+4),
// pgvector stores the dimension N directly in atttypmod — verified live:
// a vector(1024) column reports atttypmod=1024. An earlier `- 4` here was a
// varchar-convention copy/paste that made the boot guard read 1024 as 1020
// and crash-loop the materializer against a perfectly valid schema.
func (s *Store) EmbeddingDim(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var dim int
	err := s.db(ctx, true).Raw(`
		SELECT COALESCE(NULLIF(a.atttypmod, -1), 0)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = 'opportunities'
		  AND a.attname = 'embedding'
		  AND NOT a.attisdropped`).Scan(&dim).Error
	if err != nil {
		return 0, fmt.Errorf("read embedding dim: %w", err)
	}
	return dim, nil
}

// VerifyEmbeddingDim fails when the live opportunities.embedding column
// dimension differs from want (the configured EMBEDDING_DIM, which must
// equal the deployed model's native output dimension — 384 for
// multilingual-e5-small, 1024 for e5-large/bge-m3). A mismatch makes
// pgvector reject every embedding write, which UpdateEmbedding then
// soft-fails — silently dropping 100% of embeddings (the failure mode
// that left all opportunities NULL). Callers treat this as fatal at boot
// so the misconfiguration surfaces immediately instead of as an empty
// search index discovered days later.
//
// Returns nil when the column is absent (dim 0) so fresh installs and
// tests that haven't run the schema migration aren't blocked.
func (s *Store) VerifyEmbeddingDim(ctx context.Context, want int) error {
	got, err := s.EmbeddingDim(ctx)
	if err != nil {
		return err
	}
	if got == 0 || got == want {
		return nil
	}
	return fmt.Errorf(
		"embedding dimension mismatch: opportunities.embedding is vector(%d) but EMBEDDING_DIM=%d "+
			"(the deployed model must emit that many dimensions); "+
			"every embedding write would be rejected by pgvector and silently dropped. "+
			"Align the schema (run the embedding-dim migration) or set EMBEDDING_DIM/EMBEDDING_MODEL to match",
		got, want)
}

// UpdateEmbedding writes the per-canonical embedding vector into the
// opportunities row. pgvector's `vector` type round-trips through GORM
// as a `[float64,…]` literal; we hand-format because GORM doesn't have
// a built-in serializer for the type.
//
// No-op when the row doesn't exist yet (worker.canonical UPSERT hasn't
// fired for this canonical). The next CanonicalUpsertedV1 will create
// the row and a future embedding refresh will fill it in.
func (s *Store) UpdateEmbedding(ctx context.Context, canonicalID string, vector []float32) error {
	if s == nil || s.db == nil {
		return nil
	}
	if canonicalID == "" || len(vector) == 0 {
		return nil
	}
	lit := vectorLiteral(vector)
	err := s.db(ctx, false).
		Exec(`UPDATE opportunities SET embedding = ?::vector, updated_at = now() WHERE canonical_id = ?`,
			lit, canonicalID).Error
	return s.soft(ctx, err, "update_embedding", "", canonicalID)
}

// HideBySource flips hidden=true for every opportunity carrying the
// given source_id — used by SourceStoppedHandler so a kill-switched
// source's canonicals disappear from search immediately.
func (s *Store) HideBySource(ctx context.Context, sourceID, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if sourceID == "" {
		return nil
	}
	err := s.db(ctx, false).
		Exec(`UPDATE opportunities SET hidden = true, hidden_reason = ?, updated_at = now() WHERE source_id = ?`,
			reason, sourceID).Error
	return s.soft(ctx, err, "hide_by_source", "", sourceID)
}

// HideByCanonical flips hidden=true for a single canonical. Used by
// AutoFlaggedHandler when the user-flag threshold trips.
func (s *Store) HideByCanonical(ctx context.Context, canonicalID, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if canonicalID == "" {
		return nil
	}
	err := s.db(ctx, false).
		Exec(`UPDATE opportunities SET hidden = true, hidden_reason = ?, updated_at = now() WHERE canonical_id = ?`,
			reason, canonicalID).Error
	return s.soft(ctx, err, "hide_by_canonical", "", canonicalID)
}

// ExpireCanonical pushes the deadline into the past so the 1-month
// search window filters it out. Used by CanonicalExpiredHandler.
func (s *Store) ExpireCanonical(ctx context.Context, canonicalID string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	if canonicalID == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	err := s.db(ctx, false).
		Exec(`UPDATE opportunities SET deadline = ?, status = 'expired', updated_at = now() WHERE canonical_id = ?`,
			at, canonicalID).Error
	return s.soft(ctx, err, "expire_canonical", "", canonicalID)
}

// vectorLiteral renders a []float32 as pgvector's text input format
// `[v1,v2,…]`. Avoids the GORM driver's lack of a native pgvector
// serializer.
func vectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b []byte
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendFloat32(b, f)
	}
	b = append(b, ']')
	return string(b)
}

func appendFloat32(b []byte, f float32) []byte {
	// strconv.AppendFloat with bitSize=32 keeps the round-trip exact
	// while staying compact (no trailing 0s like "%g" can produce).
	return strconvAppendFloat32(b, f)
}

// GetOpportunity returns the canonical row for the given ID, or nil if
// not found. Used by CanonicalHandler as the Postgres-backed fallback
// for the cluster snapshot read when Valkey misses.
//
// Returns (nil, nil) on miss; (nil, nil) on Postgres error after a
// WARN log (soft-fail). The caller treats both as a clean miss and
// proceeds with an empty prior snapshot.
func (s *Store) GetOpportunity(ctx context.Context, canonicalID string) (*Opportunity, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var row Opportunity
	err := s.db(ctx, true).
		Table("opportunities").
		Where("canonical_id = ?", canonicalID).
		Limit(1).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		util.Log(ctx).WithError(err).
			WithField("canonical_id", canonicalID).
			Warn("variantstate: GetOpportunity failed; treating as miss")
		return nil, nil
	}
	return &row, nil
}

// HardKeyForCluster returns the hard_key tied to a cluster (used by
// admin sweep tools; not on the hot path). Convenience helper.
func (s *Store) HardKeyForCluster(ctx context.Context, canonicalID string) (string, error) {
	var row struct {
		HardKey string
	}
	err := s.db(ctx, true).
		Table("pipeline_variants").
		Select("hard_key").
		Where("canonical_id = ?", canonicalID).
		Order("ingested_at ASC").
		Limit(1).
		Take(&row).Error
	if err != nil {
		return "", fmt.Errorf("variantstate: %w", err)
	}
	return row.HardKey, nil
}
