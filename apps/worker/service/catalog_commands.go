package service

import (
	"context"
	"time"

	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
)

// catalogCommandStore is the jobqueue.Store surface the runner needs.
type catalogCommandStore interface {
	ClaimCatalogCommand(ctx context.Context, owner string, lease time.Duration) (*jobqueue.CatalogCommand, error)
	RunCatalogCommand(ctx context.Context, cmd jobqueue.CatalogCommand) (jobqueue.CommandOutcome, error)
	FailCatalogCommand(ctx context.Context, cmd jobqueue.CatalogCommand, cause error, maxAttempts int) error
	CommandBacklog(ctx context.Context, staleAfter time.Duration) (dead, stale int64, err error)
}

// backlogStaleAfter: a stop the operator was told is "queued" should be
// applied within minutes; older pending commands are alerted on.
const backlogStaleAfter = 30 * time.Minute

// backlogAlertEvery rate-limits the backlog ERROR log.
const backlogAlertEvery = 10 * time.Minute

// CatalogCommandRunner applies crawl-plane catalog commands (e.g. "hide every
// listing of a stopped source") to the product database. The crawler has no
// product-DB access, so the dual-DB worker is the only process that can.
// Run is a Frame background consumer.
type CatalogCommandRunner struct {
	store       catalogCommandStore
	owner       string
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
	lastAlert   time.Time
}

// NewCatalogCommandRunner builds a runner. Zero values get safe defaults.
func NewCatalogCommandRunner(store catalogCommandStore, poll time.Duration) *CatalogCommandRunner {
	if poll <= 0 {
		poll = 10 * time.Second
	}
	return &CatalogCommandRunner{
		store: store, owner: "catalog-" + xid.New().String(),
		poll: poll, lease: 2 * time.Minute, maxAttempts: 20,
	}
}

// Run polls until ctx is cancelled. Transient errors are logged, never
// returned (a returned error would stop the whole worker service).
func (r *CatalogCommandRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// drain applies runnable commands until none are left (bounded per tick).
func (r *CatalogCommandRunner) drain(ctx context.Context) {
	log := util.Log(ctx)
	defer r.alertBacklog(ctx)
	for i := 0; i < 50 && ctx.Err() == nil; i++ {
		cmd, err := r.store.ClaimCatalogCommand(ctx, r.owner, r.lease)
		if err != nil {
			log.WithError(err).Warn("catalog commands: claim failed")
			return
		}
		if cmd == nil {
			return
		}
		cl := log.WithField("command_id", cmd.ID).WithField("kind", cmd.Kind).WithField("source_id", cmd.SourceID)
		outcome, runErr := r.store.RunCatalogCommand(ctx, *cmd)
		if runErr != nil {
			cl.WithError(runErr).WithField("attempt", cmd.Attempt).Warn("catalog commands: run failed")
			if failErr := r.store.FailCatalogCommand(ctx, *cmd, runErr, r.maxAttempts); failErr != nil {
				cl.WithError(failErr).Error("catalog commands: recording failure failed")
			}
			continue
		}
		cl.WithField("outcome", string(outcome)).Info("catalog commands: processed")
	}
}

// alertBacklog ERROR-logs (for log-based alerting) dead or long-pending
// commands: each is a stop whose listings may still be visible.
func (r *CatalogCommandRunner) alertBacklog(ctx context.Context) {
	if ctx.Err() != nil || time.Since(r.lastAlert) < backlogAlertEvery {
		return
	}
	dead, stale, err := r.store.CommandBacklog(ctx, backlogStaleAfter)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("catalog commands: backlog check failed")
		return
	}
	if dead == 0 && stale == 0 {
		return
	}
	r.lastAlert = time.Now()
	util.Log(ctx).WithField("dead", dead).WithField("pending_over_30m", stale).
		Error("catalog commands: backlog — stopped sources may still show listings; check catalog_commands")
}
