package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	workflowv1 "github.com/antinvestor/service-trustage/gen/go/workflow/v1"

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
		wantHasEvery string
	}{
		{3600, "*/12"},  // 1h → floored to 12h
		{14400, "*/12"}, // 4h → floored to 12h
		{43200, "*/12"}, // 12h
		{86400, ""},     // daily — "m h * * *"
		{1800, "*/12"},  // 30m → floored to 12h
	}
	for _, c := range cases {
		got := cronForInterval(c.intervalSec, "src-"+string(rune(c.intervalSec)))
		if c.wantHasEvery != "" && !strings.Contains(got, c.wantHasEvery) {
			t.Errorf("interval %d → cron %q, want containing %q", c.intervalSec, got, c.wantHasEvery)
		}
		if len(strings.Fields(got)) != 5 {
			t.Errorf("interval %d → cron %q is not a 5-field cron", c.intervalSec, got)
		}
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
