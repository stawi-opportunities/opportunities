package service

import (
	"context"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/variantstate"
)

// Reaper periodically self-heals variants that wedged mid-chain. The five
// pipeline stages multiplex onto ONE shared NATS events consumer with no
// app-level max-deliver/DLQ; if NATS drops a redelivery (its own
// max-deliver) or an Emit silently fails, a variant can sit at an
// intermediate stage forever. The reaper sweeps pipeline_variants for rows
// stuck past a threshold and re-emits the appropriate stage event to
// re-drive them.
//
// Re-drive is best-effort: pipeline_variants does NOT store the per-kind
// Attributes map (that lives in the cluster snapshot cache), so the
// re-emitted envelope carries only the identity fields from the row. The
// downstream handler reconstructs Attributes from its cluster snapshot
// (canonical/dedup both fall back to the cached snapshot, or an empty one).
// The point is to advance a wedged row, not to replay its full payload.
type Reaper struct {
	svc      *frame.Service
	store    *variantstate.Store
	interval time.Duration
	// threshold is how long a row must sit at stage_at before it's
	// considered stuck.
	threshold time.Duration
	// batch bounds how many rows a single sweep re-drives.
	batch int
}

// Default reaper tuning. A 2-min tick with a 10-min stuck threshold means a
// wedged variant self-heals within ~12 min worst case, well under the human
// "0 published" detection window.
const (
	reaperInterval  = 2 * time.Minute
	reaperThreshold = 10 * time.Minute
	reaperBatch     = 500
)

// NewReaper builds a reaper with the default tuning. store may be nil (no
// datastore wired) — in that case Run is a no-op after logging once.
func NewReaper(svc *frame.Service, store *variantstate.Store) *Reaper {
	return &Reaper{
		svc:       svc,
		store:     store,
		interval:  reaperInterval,
		threshold: reaperThreshold,
		batch:     reaperBatch,
	}
}

// stuckExcludeStages are the current_stage values the reaper skips:
// terminal states (published/flagged/rejected) and canonical (publish runs
// off the canonical fan-out and is re-driven by re-emitting the canonical
// event, but per the hardening spec the reaper does not chase canonical
// rows — a failed R2 publish leaves the row at canonical and is recovered
// by the next CanonicalUpsertedV1 when R2 is healthy).
var stuckExcludeStages = []string{
	variantstate.StagePublished,
	variantstate.StageFlagged,
	variantstate.StageRejected,
	variantstate.StageCanonical,
}

// Run blocks, sweeping on each tick until ctx is cancelled. Intended to be
// launched in its own goroutine before svc.Run.
func (r *Reaper) Run(ctx context.Context) {
	if r.store == nil {
		util.Log(ctx).Warn("reaper: no variant store wired; stuck-variant self-heal disabled")
		return
	}
	util.Log(ctx).
		WithField("interval", r.interval.String()).
		WithField("threshold", r.threshold.String()).
		WithField("batch", r.batch).
		Info("reaper: started")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			util.Log(ctx).Info("reaper: stopping")
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

// sweep finds stuck rows and re-drives each one. Errors are logged and
// swallowed so one bad row never aborts the sweep.
func (r *Reaper) sweep(ctx context.Context) {
	rows, err := r.store.ListStuck(ctx, r.threshold, stuckExcludeStages, r.batch)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("reaper: ListStuck failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	var redrove int
	for i := range rows {
		if r.redrive(ctx, rows[i]) {
			redrove++
		}
	}
	util.Log(ctx).
		WithField("stuck", len(rows)).
		WithField("redrove", redrove).
		Info("reaper: sweep complete")
}

// redrive re-emits the stage event matching v.CurrentStage. Returns true
// when an event was emitted. Stage → re-emit topic mapping mirrors the
// consume edges: a row stuck at stage S is re-driven by re-publishing the
// event that produced S, so the handler that advances S→S+1 runs again.
func (r *Reaper) redrive(ctx context.Context, v variantstate.Variant) bool {
	switch v.CurrentStage {
	case variantstate.StageIngested:
		// Re-emit VariantsNormalized's input. We don't keep the original
		// ingested payload, but re-emitting the ingested topic with the
		// identity fields lets normalize run again.
		return r.emit(ctx, v, eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
			VariantID: v.VariantID,
			SourceID:  v.SourceID,
			HardKey:   v.HardKey,
			Kind:      v.Kind,
		})
	case variantstate.StageNormalized:
		return r.emit(ctx, v, eventsv1.TopicVariantsNormalized, eventsv1.VariantNormalizedV1{
			VariantID:    v.VariantID,
			SourceID:     v.SourceID,
			HardKey:      v.HardKey,
			Kind:         v.Kind,
			NormalizedAt: time.Now().UTC(),
		})
	case variantstate.StageValidated:
		return r.emit(ctx, v, eventsv1.TopicVariantsValidated, eventsv1.VariantValidatedV1{
			VariantID:   v.VariantID,
			SourceID:    v.SourceID,
			HardKey:     v.HardKey,
			Kind:        v.Kind,
			Valid:       true,
			ValidatedAt: time.Now().UTC(),
		})
	case variantstate.StageClustered:
		out := eventsv1.VariantClusteredV1{
			VariantID:   v.VariantID,
			HardKey:     v.HardKey,
			Kind:        v.Kind,
			ClusteredAt: time.Now().UTC(),
		}
		if v.CanonicalID != nil {
			out.OpportunityID = *v.CanonicalID
		}
		return r.emit(ctx, v, eventsv1.TopicVariantsClustered, out)
	default:
		// Unknown / legacy stage (e.g. manticore): nothing safe to re-drive.
		util.Log(ctx).
			WithField("variant_id", v.VariantID).
			WithField("current_stage", v.CurrentStage).
			Debug("reaper: no re-drive mapping for stage; skipping")
		return false
	}
}

// emit wraps the payload in an envelope and publishes it on the events bus.
func (r *Reaper) emit(ctx context.Context, v variantstate.Variant, topic string, payload any) bool {
	env := eventsv1.NewEnvelope(topic, payload)
	if err := r.svc.EventsManager().Emit(ctx, topic, env); err != nil {
		util.Log(ctx).WithError(err).
			WithField("variant_id", v.VariantID).
			WithField("topic", topic).
			Warn("reaper: re-emit failed")
		return false
	}
	util.Log(ctx).
		WithField("variant_id", v.VariantID).
		WithField("current_stage", v.CurrentStage).
		WithField("topic", topic).
		Debug("reaper: re-drove stuck variant")
	return true
}
