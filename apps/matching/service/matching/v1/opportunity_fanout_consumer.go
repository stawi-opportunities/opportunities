package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/matching"
	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

// OpportunityFanOutConsumerDeps wires Path A (new opportunity → candidates).
type OpportunityFanOutConsumerDeps struct {
	KNN      *matching.KNN
	Store    *matching.Store
	EventLog *matching.EventLog
	Reranker matching.Reranker
	Weights  matching.Weights
	DailyCap matching.DailyCapQuery
	// Svc emits domain events for bus consumers / analytics.
	Svc *frame.Service
	// DB looks up match_alerts / profile_id.
	DB *sql.DB
	// NotificationCli is the platform notification service client (profile-style).
	NotificationCli notificationv1connect.NotificationServiceClient
	// Templates are MESSAGE_TEMPLATE_* names from config.
	Templates notify.Templates
	// PublicSiteURL for dashboard links in template variables.
	PublicSiteURL string
	// DefaultMinScore floors when index min_score is unset (unused in FanOut —
	// candidates carry MinScore on the KNN hit). Kept for symmetry/logging.
	DefaultMinScore float64
}

// OpportunityFanOutConsumer drains SubjectOpportunityFanOut and runs FanOut.
type OpportunityFanOutConsumer struct {
	deps OpportunityFanOutConsumerDeps
}

// NewOpportunityFanOutConsumer constructs the queue worker.
func NewOpportunityFanOutConsumer(d OpportunityFanOutConsumerDeps) *OpportunityFanOutConsumer {
	if d.Weights == (matching.Weights{}) {
		d.Weights = matching.DefaultWeights()
	}
	if d.Reranker == nil {
		d.Reranker = matching.NoopReranker{}
	}
	return &OpportunityFanOutConsumer{deps: d}
}

// Name is the queue subject.
func (c *OpportunityFanOutConsumer) Name() string { return eventsv1.SubjectOpportunityFanOut }

// Handle implements queue.SubscribeWorker.
func (c *OpportunityFanOutConsumer) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("opportunity_fanout: empty payload")
	}
	var env eventsv1.Envelope[eventsv1.OpportunityFanOutV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("opportunity_fanout: decode: %w", err)
	}
	job := env.Payload
	if strings.TrimSpace(job.OpportunityID) == "" {
		return fmt.Errorf("opportunity_fanout: opportunity_id required")
	}
	if len(job.Embedding) == 0 {
		util.Log(ctx).WithField("opportunity_id", job.OpportunityID).
			Info("opportunity_fanout: empty embedding; skip")
		return nil
	}

	kind := job.Kind
	if kind == "" {
		kind = "job"
	}
	var salaryMax *int
	if job.AmountMax != nil {
		v := int(*job.AmountMax)
		salaryMax = &v
	}
	firstSeen := time.Now().UTC()
	if job.PostedAt != "" {
		if t, err := time.Parse(time.RFC3339, job.PostedAt); err == nil {
			firstSeen = t
		}
	}
	queryText := strings.TrimSpace(job.Title + " " + job.Description)
	if len(queryText) > 2000 {
		queryText = queryText[:2000]
	}

	res, err := matching.FanOut(ctx, matching.FanOutInput{
		CanonicalID:   job.OpportunityID,
		OpportunityID: job.OpportunityID,
		Kind:          kind,
		Country:       job.Country,
		SalaryMaxUSD:  salaryMax,
		Embedding:     job.Embedding,
		FirstSeenAt:   firstSeen,
		QueryText:     queryText,
	}, matching.FanOutDeps{
		KNN:      c.deps.KNN,
		Store:    c.deps.Store,
		EventLog: c.deps.EventLog,
		Reranker: c.deps.Reranker,
		Weights:  c.deps.Weights,
		DailyCap: c.deps.DailyCap,
	})
	if err != nil {
		return fmt.Errorf("opportunity_fanout: fanout: %w", err)
	}

	util.Log(ctx).WithField("opportunity_id", job.OpportunityID).
		WithField("scanned", res.CandidatesScanned).
		WithField("written", res.MatchesWritten).
		WithField("new", len(res.NewMatches)).
		Info("opportunity_fanout: path A complete")

	// Collect always; user notify only when match_alerts (digests cover the rest).
	// Delivery is always NotificationService.Send — never product-side email.
	c.notifyMatches(ctx, res, job)
	return nil
}

func (c *OpportunityFanOutConsumer) notifyMatches(ctx context.Context, res matching.FanOutResult, job eventsv1.OpportunityFanOutV1) {
	if len(res.NewMatches) == 0 {
		return
	}
	site := strings.TrimRight(c.deps.PublicSiteURL, "/")
	for _, m := range res.NewMatches {
		row := eventsv1.MatchRow{
			CanonicalID: m.OpportunityID,
			Title:       job.Title,
			Company:     job.IssuingEntity,
			ApplyURL:    m.ApplyURL,
			Score:       m.Score,
		}
		// Domain event for analytics / bus bridges.
		if c.deps.Svc != nil {
			env := eventsv1.NewEnvelope(eventsv1.TopicCandidateMatchesReady, eventsv1.MatchesReadyV1{
				CandidateID:  m.CandidateID,
				MatchBatchID: res.RunID,
				Matches:      []eventsv1.MatchRow{row},
			})
			if emitErr := c.deps.Svc.EventsManager().Emit(ctx, eventsv1.TopicCandidateMatchesReady, env); emitErr != nil {
				util.Log(ctx).WithError(emitErr).WithField("candidate_id", m.CandidateID).
					Debug("opportunity_fanout: domain event emit failed")
			}
		}

		// Per-match user notification only when opted into match_alerts.
		if c.deps.DB != nil {
			ok, err := candidateWantsMatchAlerts(ctx, c.deps.DB, m.CandidateID)
			if err != nil || !ok {
				continue
			}
		} else {
			continue
		}
		if c.deps.NotificationCli == nil {
			util.Log(ctx).WithField("candidate_id", m.CandidateID).
				Warn("opportunity_fanout: notification client nil — match alert not sent")
			continue
		}
		profileID := notify.ProfileID(ctx, c.deps.DB, m.CandidateID)
		_ = notify.Send(ctx, c.deps.NotificationCli, notify.Message{
			Template:  c.deps.Templates.Ready(),
			ProfileID: profileID,
			Variables: map[string]any{
				"candidate_id":   m.CandidateID,
				"match_batch_id": res.RunID,
				"count":          float64(1),
				"dashboard_url":  site + "/dashboard/#matches",
				"matches": []any{
					map[string]any{
						"canonical_id": row.CanonicalID,
						"title":        row.Title,
						"company":      row.Company,
						"apply_url":    row.ApplyURL,
						"score":        row.Score,
					},
				},
			},
			Priority:    notificationv1.PRIORITY_HIGH,
			PrioritySet: true,
		})
	}
}

// candidateWantsMatchAlerts is true only when match_alerts is explicitly true.
// Default is false — digests cover summaries unless the user opts into
// every-match alerts.
func candidateWantsMatchAlerts(ctx context.Context, db *sql.DB, candidateID string) (bool, error) {
	var alerts bool
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(match_alerts, false) FROM candidate_profiles WHERE id = $1`,
		candidateID,
	).Scan(&alerts)
	return err == nil && alerts, err
}
