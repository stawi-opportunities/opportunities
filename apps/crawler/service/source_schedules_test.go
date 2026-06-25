package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	workflowv1 "github.com/antinvestor/service-trustage/gen/go/workflow/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// fakeWorkflowClient records the schedule RPCs the sync logic makes and serves
// a canned set of "existing" active workflows keyed by name.
type fakeWorkflowClient struct {
	existing map[string]string // name -> id (active)
	crons    map[string]string // name -> existing cron (for drift tests; "" → drift)
	created  []string          // workflow names created
	archived []string          // workflow ids archived
}

func (f *fakeWorkflowClient) ListWorkflows(_ context.Context, req *connect.Request[workflowv1.ListWorkflowsRequest]) (*connect.Response[workflowv1.ListWorkflowsResponse], error) {
	name := req.Msg.GetName()
	items := []*workflowv1.WorkflowDefinition{}
	if id, ok := f.existing[name]; ok {
		dsl, _ := structpb.NewStruct(map[string]any{
			"name":      name,
			"schedules": []any{map[string]any{"cron_expr": f.crons[name]}},
		})
		items = append(items, &workflowv1.WorkflowDefinition{
			Id: id, Name: name, Status: workflowv1.WorkflowStatus_WORKFLOW_STATUS_ACTIVE, Dsl: dsl,
		})
	}
	return connect.NewResponse(&workflowv1.ListWorkflowsResponse{Items: items}), nil
}

func (f *fakeWorkflowClient) CreateWorkflow(_ context.Context, req *connect.Request[workflowv1.CreateWorkflowRequest]) (*connect.Response[workflowv1.CreateWorkflowResponse], error) {
	name := req.Msg.GetDsl().GetFields()["name"].GetStringValue()
	f.created = append(f.created, name)
	return connect.NewResponse(&workflowv1.CreateWorkflowResponse{
		Workflow: &workflowv1.WorkflowDefinition{Id: "wf_" + name, Name: name},
	}), nil
}

func (f *fakeWorkflowClient) ActivateWorkflow(_ context.Context, _ *connect.Request[workflowv1.ActivateWorkflowRequest]) (*connect.Response[workflowv1.ActivateWorkflowResponse], error) {
	return connect.NewResponse(&workflowv1.ActivateWorkflowResponse{}), nil
}

func (f *fakeWorkflowClient) ArchiveWorkflow(_ context.Context, req *connect.Request[workflowv1.ArchiveWorkflowRequest]) (*connect.Response[workflowv1.ArchiveWorkflowResponse], error) {
	f.archived = append(f.archived, req.Msg.GetId())
	return connect.NewResponse(&workflowv1.ArchiveWorkflowResponse{}), nil
}

func src(id string, status domain.SourceStatus, intervalSec int) *domain.Source {
	s := &domain.Source{Type: domain.SourceGenericHTML, BaseURL: "https://x/" + id, Status: status, CrawlIntervalSec: intervalSec}
	s.ID = id
	return s
}

func TestCronForInterval(t *testing.T) {
	cases := []struct {
		intervalSec int
		wantFires   int // expected number of hour entries in the hour field
	}{
		{3600, 2},  // 1h → floored to 12h → two fires/day
		{14400, 2}, // 4h → floored to 12h
		{43200, 2}, // 12h
		{86400, 1}, // daily — "m h * * *"
		{1800, 2},  // 30m → floored to 12h
	}
	for _, c := range cases {
		got := cronForInterval(c.intervalSec, "src-"+string(rune(c.intervalSec)))
		fields := strings.Fields(got)
		if len(fields) != 5 {
			t.Fatalf("interval %d → cron %q is not a 5-field cron", c.intervalSec, got)
		}
		hours := strings.Split(fields[1], ",")
		if len(hours) != c.wantFires {
			t.Errorf("interval %d → cron %q, want %d hour entries, got %d", c.intervalSec, got, c.wantFires, len(hours))
		}
		for _, h := range hours {
			n, err := strconv.Atoi(h)
			if err != nil || n < 0 || n > 23 {
				t.Errorf("interval %d → cron %q has invalid hour %q", c.intervalSec, got, h)
			}
		}
	}
}

// TestCronForInterval_SpreadsHourPhase guards against the thundering herd:
// a fleet of 12h-interval sources must NOT all fire in the same two hours
// (the pre-fix "M */12 * * *" put ~90 sources inside hours 0 and 12 UTC,
// so one closed-gate window cost every source its full interval).
func TestCronForInterval_SpreadsHourPhase(t *testing.T) {
	firstHours := map[string]bool{}
	for i := 0; i < 60; i++ {
		got := cronForInterval(43200, fmt.Sprintf("source-%d", i))
		hourField := strings.Fields(got)[1]
		firstHours[strings.Split(hourField, ",")[0]] = true
		// Consecutive fires must be exactly 12h apart so the effective
		// interval is preserved.
		hours := strings.Split(hourField, ",")
		if len(hours) == 2 {
			a, _ := strconv.Atoi(hours[0])
			b, _ := strconv.Atoi(hours[1])
			if b-a != 12 {
				t.Errorf("cron %q: fires %d and %d are not 12h apart", got, a, b)
			}
		}
	}
	if len(firstHours) < 8 {
		t.Errorf("60 sources spread across only %d distinct phase hours; want >= 8", len(firstHours))
	}
}

func TestEnsureSourceSchedule_CreatesWhenAbsent(t *testing.T) {
	f := &fakeWorkflowClient{existing: map[string]string{}}
	if err := EnsureSourceSchedule(context.Background(), f, src("s1", domain.SourceActive, 3600), "http://crawler"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(f.created) != 1 || f.created[0] != workflowName("s1") {
		t.Fatalf("expected create of %s, got %v", workflowName("s1"), f.created)
	}
}

func TestEnsureSourceSchedule_SkipsWhenPresent(t *testing.T) {
	name := workflowName("s1")
	f := &fakeWorkflowClient{
		existing: map[string]string{name: "wf_s1"},
		crons:    map[string]string{name: cronForInterval(3600, "s1")}, // matching cadence
	}
	if err := EnsureSourceSchedule(context.Background(), f, src("s1", domain.SourceActive, 3600), "http://crawler"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(f.created) != 0 || len(f.archived) != 0 {
		t.Fatalf("expected no-op (cron matches), got created=%v archived=%v", f.created, f.archived)
	}
}

func TestEnsureSourceSchedule_RecreatesOnCadenceDrift(t *testing.T) {
	name := workflowName("s1")
	f := &fakeWorkflowClient{
		existing: map[string]string{name: "wf_s1"},
		crons:    map[string]string{name: "5 */1 * * *"}, // stale 1h cron (pre-floor)
	}
	if err := EnsureSourceSchedule(context.Background(), f, src("s1", domain.SourceActive, 3600), "http://crawler"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(f.archived) != 1 || len(f.created) != 1 {
		t.Fatalf("expected archive+recreate on drift, got created=%v archived=%v", f.created, f.archived)
	}
}

func TestRemoveSourceSchedule_ArchivesActive(t *testing.T) {
	f := &fakeWorkflowClient{existing: map[string]string{workflowName("s1"): "wf_s1"}}
	if err := RemoveSourceSchedule(context.Background(), f, "s1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(f.archived) != 1 || f.archived[0] != "wf_s1" {
		t.Fatalf("expected archive of wf_s1, got %v", f.archived)
	}
}

func TestReconcile_EnsuresActiveArchivesInactive(t *testing.T) {
	f := &fakeWorkflowClient{existing: map[string]string{
		workflowName("paused1"): "wf_paused1", // has a stale schedule, should be archived
	}}
	sources := []*domain.Source{
		src("active1", domain.SourceActive, 3600),
		src("active2", domain.SourceDegraded, 7200),
		src("paused1", domain.SourcePaused, 3600),
	}
	ensured, archived, failed := ReconcileSourceSchedules(context.Background(), f, sources, "http://crawler")
	if failed != 0 {
		t.Fatalf("failed=%d, want 0", failed)
	}
	if ensured != 2 {
		t.Fatalf("ensured=%d, want 2 (active1, active2)", ensured)
	}
	if archived != 1 {
		t.Fatalf("archived=%d, want 1 (paused1)", archived)
	}
	if len(f.archived) != 1 || f.archived[0] != "wf_paused1" {
		t.Fatalf("expected archive of wf_paused1, got %v", f.archived)
	}
}
