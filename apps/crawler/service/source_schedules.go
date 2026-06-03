package service

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"connectrpc.com/connect"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/types/known/structpb"

	workflowv1 "github.com/antinvestor/service-trustage/gen/go/workflow/v1"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SchedulePrefix namespaces every per-source crawl workflow in Trustage so the
// reconcile pass can tell our schedules apart from other workflows.
const SchedulePrefix = "opportunities.source.crawl."

// WorkflowClient is the subset of Trustage's workflow RPC surface the
// per-source schedule sync needs. *workflowv1connect.WorkflowServiceClient
// satisfies it.
type WorkflowClient interface {
	ListWorkflows(context.Context, *connect.Request[workflowv1.ListWorkflowsRequest]) (*connect.Response[workflowv1.ListWorkflowsResponse], error)
	CreateWorkflow(context.Context, *connect.Request[workflowv1.CreateWorkflowRequest]) (*connect.Response[workflowv1.CreateWorkflowResponse], error)
	ActivateWorkflow(context.Context, *connect.Request[workflowv1.ActivateWorkflowRequest]) (*connect.Response[workflowv1.ActivateWorkflowResponse], error)
	ArchiveWorkflow(context.Context, *connect.Request[workflowv1.ArchiveWorkflowRequest]) (*connect.Response[workflowv1.ArchiveWorkflowResponse], error)
}

// scheduleActive reports whether a source should have a live crawl schedule.
// Only active/degraded sources are crawled; everything else (paused, stopped,
// rejected, …) must NOT have a Trustage schedule.
func scheduleActive(s *domain.Source) bool {
	return s.Status == domain.SourceActive || s.Status == domain.SourceDegraded
}

// workflowName is the deterministic Trustage workflow name for a source.
func workflowName(sourceID string) string { return SchedulePrefix + sourceID }

// jitter returns a stable pseudo-random integer in [0, n) derived from the
// source ID, so per-source schedules spread across the window instead of all
// firing on the same minute/hour (thundering herd).
func jitter(sourceID string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(sourceID))
	return int(h.Sum32() % uint32(n))
}

// cronForInterval derives a cron expression from a source's crawl interval,
// jittered per source. Fixed cadence (no adaptive scoring): a source configured
// to recrawl every N hours fires every N hours at a stable per-source minute.
func cronForInterval(crawlIntervalSec int, sourceID string) string {
	if crawlIntervalSec <= 0 {
		crawlIntervalSec = 3600
	}
	minute := jitter(sourceID, 60)
	hours := crawlIntervalSec / 3600
	switch {
	case hours < 1:
		// Sub-hourly: run every `step` minutes (clamped to [15, 30] so we
		// never hammer a source faster than the platform floor).
		step := crawlIntervalSec / 60
		if step < 15 {
			step = 15
		}
		if step > 30 {
			step = 30
		}
		return fmt.Sprintf("*/%d * * * *", step)
	case hours >= 24:
		// Daily (or coarser): one fire per day at a jittered hour:minute.
		return fmt.Sprintf("%d %d * * *", minute, jitter(sourceID, 24))
	default:
		// Every `hours` hours at a jittered minute.
		return fmt.Sprintf("%d */%d * * *", minute, hours)
	}
}

// buildSourceCrawlDSL builds the Trustage workflow definition (DSL) for a
// single source: a scheduled HTTP POST to the crawler's per-source crawl
// endpoint. Mirrors the shape of definitions/trustage/scheduler-tick.json but
// scoped to one source and fired at the source's own cadence.
func buildSourceCrawlDSL(s *domain.Source, crawlBaseURL string) (*structpb.Struct, error) {
	name := workflowName(s.ID)
	url := fmt.Sprintf("%s/admin/sources/%s/crawl", crawlBaseURL, s.ID)
	m := map[string]any{
		"version":     "1.0",
		"name":        name,
		"description": fmt.Sprintf("Crawl source %s (%s) on its own cadence.", s.ID, s.BaseURL),
		"timeout":     "2m",
		"on_error":    map[string]any{"action": "abort"},
		"steps": []any{
			map[string]any{
				"id":      "crawl_source",
				"type":    "call",
				"name":    "Invoke per-source crawl",
				"timeout": "90s",
				"retry": map[string]any{
					"max_attempts":     float64(3),
					"backoff_strategy": "exponential",
					"initial_backoff":  "10s",
				},
				"call": map[string]any{
					"action": "http.request",
					"input": map[string]any{
						"url":     url,
						"method":  "POST",
						"headers": map[string]any{"Content-Type": "application/json"},
						"body":    map[string]any{},
					},
					"output_var": "crawl_result",
				},
			},
		},
		"schedules": []any{
			map[string]any{
				"name":      name + ".default",
				"cron_expr": cronForInterval(s.CrawlIntervalSec, s.ID),
				"timezone":  "UTC",
			},
		},
	}
	return structpb.NewStruct(m)
}

// EnsureSourceSchedule makes the per-source crawl workflow exist and be active
// in Trustage. Idempotent AND concurrency-safe: multiple crawler replicas can
// run this for the same source at once (boot reconcile) — the create/activate
// races converge because we (a) look up the workflow by name across ALL
// statuses, (b) just (re)activate one that already exists, and (c) treat a
// create that loses the unique-name race as "already exists" and activate the
// winner instead of erroring.
func EnsureSourceSchedule(ctx context.Context, client WorkflowClient, s *domain.Source, crawlBaseURL string) error {
	name := workflowName(s.ID)

	id, active, err := findWorkflow(ctx, client, name)
	if err != nil {
		return err
	}
	if active {
		return nil // already scheduled
	}
	if id != "" {
		return activate(ctx, client, name, id) // exists but draft/archived → activate
	}

	dsl, err := buildSourceCrawlDSL(s, crawlBaseURL)
	if err != nil {
		return fmt.Errorf("schedules: build dsl %s: %w", name, err)
	}
	createResp, err := client.CreateWorkflow(ctx, connect.NewRequest(&workflowv1.CreateWorkflowRequest{Dsl: dsl}))
	if err != nil {
		if !isAlreadyExistsErr(err) {
			return fmt.Errorf("schedules: create %s: %w", name, err)
		}
		// Lost the create race to another replica; activate the winner.
		id, active, lerr := findWorkflow(ctx, client, name)
		if lerr != nil {
			return lerr
		}
		if active || id == "" {
			return nil
		}
		return activate(ctx, client, name, id)
	}
	return activate(ctx, client, name, createResp.Msg.GetWorkflow().GetId())
}

// findWorkflow looks up a workflow by exact name across all statuses. Returns
// the id of a usable (non-archived) one and whether it is already active.
func findWorkflow(ctx context.Context, client WorkflowClient, name string) (id string, active bool, err error) {
	resp, err := client.ListWorkflows(ctx, connect.NewRequest(&workflowv1.ListWorkflowsRequest{Name: name}))
	if err != nil {
		return "", false, fmt.Errorf("schedules: list %s: %w", name, err)
	}
	for _, it := range resp.Msg.GetItems() {
		if it.GetName() != name {
			continue
		}
		switch it.GetStatus() {
		case workflowv1.WorkflowStatus_WORKFLOW_STATUS_ACTIVE:
			return it.GetId(), true, nil
		case workflowv1.WorkflowStatus_WORKFLOW_STATUS_ARCHIVED:
			continue // can't reactivate an archived def; a fresh create handles it
		default:
			id = it.GetId() // draft/unspecified → activatable
		}
	}
	return id, false, nil
}

// activate flips a workflow active, tolerating the not-found race (a concurrent
// reconcile may have just (re)created it under a new id).
func activate(ctx context.Context, client WorkflowClient, name, id string) error {
	if id == "" {
		return nil
	}
	if _, err := client.ActivateWorkflow(ctx, connect.NewRequest(&workflowv1.ActivateWorkflowRequest{Id: id})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}
		return fmt.Errorf("schedules: activate %s (id=%s): %w", name, id, err)
	}
	return nil
}

// RemoveSourceSchedule archives every active per-source crawl workflow for the
// source so Trustage stops firing it. Idempotent (no-op when none exist).
func RemoveSourceSchedule(ctx context.Context, client WorkflowClient, sourceID string) error {
	name := workflowName(sourceID)
	listResp, err := client.ListWorkflows(ctx, connect.NewRequest(&workflowv1.ListWorkflowsRequest{Name: name}))
	if err != nil {
		return fmt.Errorf("schedules: list %s: %w", name, err)
	}
	for _, it := range listResp.Msg.GetItems() {
		if it.GetName() != name || it.GetStatus() == workflowv1.WorkflowStatus_WORKFLOW_STATUS_ARCHIVED {
			continue
		}
		if _, err := client.ArchiveWorkflow(ctx, connect.NewRequest(&workflowv1.ArchiveWorkflowRequest{
			Id: it.GetId(),
		})); err != nil {
			return fmt.Errorf("schedules: archive %s (id=%s): %w", name, it.GetId(), err)
		}
	}
	return nil
}

// ReconcileSourceSchedules drives every source's Trustage schedule to match its
// status: active/degraded sources get a live schedule, everything else is
// archived. Source-status-driven so add/enable creates and disable/stop/remove
// archives — without enumerating all of Trustage. Best-effort per source: one
// failure is logged and reconcile continues so a single bad source can't block
// the rest. Returns the (ensured, archived, failed) counts.
func ReconcileSourceSchedules(ctx context.Context, client WorkflowClient, sources []*domain.Source, crawlBaseURL string) (ensured, archived, failed int) {
	log := util.Log(ctx)
	for _, s := range sources {
		var err error
		if scheduleActive(s) {
			if err = EnsureSourceSchedule(ctx, client, s, crawlBaseURL); err == nil {
				ensured++
			}
		} else {
			if err = RemoveSourceSchedule(ctx, client, s.ID); err == nil {
				archived++
			}
		}
		if err != nil {
			failed++
			log.WithError(err).WithField("source_id", s.ID).Warn("schedules: reconcile source failed")
		}
	}
	log.WithField("ensured", ensured).WithField("archived", archived).WithField("failed", failed).
		Info("schedules: reconcile complete")
	return ensured, archived, failed
}

func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		return true
	}
	// Trustage surfaces the unique-name collision as an internal error carrying
	// the Postgres unique-violation text rather than CodeAlreadyExists.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "23505")
}
