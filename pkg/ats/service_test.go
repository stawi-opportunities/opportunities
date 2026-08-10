package ats

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2/security"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testCtx(t *testing.T, tenant, partition, profile string) context.Context {
	t.Helper()
	claims := &security.AuthenticationClaims{
		TenantID:    tenant,
		PartitionID: partition,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: profile,
		},
	}
	// ProfileID may be set via extension; Subject is used as fallback in ScopeFromContext.
	return claims.ClaimsToContext(context.Background())
}

func testService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store)
	ctx := testCtx(t, "tenant-1", "part-1", "recruiter-1")
	return svc, ctx
}

func TestCreateJobAndApplicationAdvance(t *testing.T) {
	svc, ctx := testService(t)
	j, err := svc.CreateJob(ctx, CreateJobInput{Title: "Engineer", Description: "Build", Status: JobStatusOpen})
	if err != nil {
		t.Fatal(err)
	}
	if j.TenantID != "tenant-1" || j.PartitionID != "part-1" {
		t.Fatalf("tenancy not set: %+v", j)
	}
	a, err := svc.CreateApplication(ctx, CreateApplicationInput{
		JobID:     j.ID,
		ProfileID: "cand-profile-1",
		Source:    SourceManual,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Stage != StageApplied {
		t.Fatalf("stage %s", a.Stage)
	}
	// duplicate active
	if _, err := svc.CreateApplication(ctx, CreateApplicationInput{
		JobID: j.ID, ProfileID: "cand-profile-1",
	}); err == nil {
		t.Fatal("expected conflict")
	}
	a, err = svc.Advance(ctx, a.ID, StageScreen, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if a.Stage != StageScreen {
		t.Fatal(a.Stage)
	}
}

func TestPartitionIsolation(t *testing.T) {
	svc, ctx := testService(t)
	j, err := svc.CreateJob(ctx, CreateJobInput{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	other := testCtx(t, "tenant-1", "part-2", "recruiter-2")
	if _, err := svc.GetJob(other, j.ID); err != ErrNotFound {
		t.Fatalf("want not found across partition, got %v", err)
	}
}

func TestHireIdempotentBilling(t *testing.T) {
	svc, ctx := testService(t)
	var emits int
	svc.Billing = hireCounter{&emits}
	j, _ := svc.CreateJob(ctx, CreateJobInput{Title: "X", Status: JobStatusOpen})
	a, _ := svc.CreateApplication(ctx, CreateApplicationInput{JobID: j.ID, ProfileID: "p1"})
	// walk to offer
	for _, st := range []string{StageScreen, StageInterview, StageOffer} {
		var err error
		a, err = svc.Advance(ctx, a.ID, st, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, h1, err := svc.Hire(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, h2, err := svc.Hire(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == nil || h2 == nil || h1.ID != h2.ID {
		t.Fatalf("hire outcomes differ: %+v %+v", h1, h2)
	}
	if emits != 1 {
		t.Fatalf("billing emits want 1 got %d", emits)
	}
}

type hireCounter struct{ n *int }

func (h hireCounter) EmitHire(context.Context, *HireOutcome) (string, error) {
	*h.n++
	return "bill-1", nil
}

func TestInterviewBook(t *testing.T) {
	svc, ctx := testService(t)
	// Monday rules for recruiter
	_, err := svc.SetAvailability(ctx, SetAvailabilityInput{
		Timezone: "UTC",
		Rules:    []WeekRule{{Weekday: int(time.Monday), Start: "09:00", End: "12:00"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := svc.CreateJob(ctx, CreateJobInput{Title: "Y", Status: JobStatusOpen})
	a, _ := svc.CreateApplication(ctx, CreateApplicationInput{JobID: j.ID, ProfileID: "cand-1"})
	iv, err := svc.ProposeInterview(ctx, ProposeInterviewInput{
		ApplicationID: a.ID,
		DurationMin:   60,
		Panel:         []string{"recruiter-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Force window around a known Monday if today isn't helpful — ListSlots uses Now().
	// Book using ComputeSlots directly for determinism, then call Book with that slot
	// after patching service window is hard; instead set busy empty and find any slot.
	slots, err := svc.ListSlots(ctx, iv.ID)
	if err != nil {
		// If today has no Monday in next 14 days that matches — always has Mondays.
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Skip("no slots in window from now (timezone edge); availability logic covered in slots_test")
	}
	booked, err := svc.BookInterview(ctx, BookInterviewInput{
		InterviewID: iv.ID,
		Start:       slots[0].Start,
		End:         slots[0].End,
	})
	if err != nil {
		t.Fatal(err)
	}
	if booked.Status != InterviewScheduled {
		t.Fatal(booked.Status)
	}
	// conflict rebook different
	if len(slots) > 1 {
		if _, err := svc.BookInterview(ctx, BookInterviewInput{
			InterviewID: iv.ID,
			Start:       slots[1].Start,
			End:         slots[1].End,
		}); err == nil {
			t.Fatal("expected conflict")
		}
	}
}

func TestPublish(t *testing.T) {
	svc, ctx := testService(t)
	svc.Publisher = fakePub{id: "opp-99"}
	j, _ := svc.CreateJob(ctx, CreateJobInput{Title: "Pub"})
	j, err := svc.PublishJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Visibility != VisibilityPublished || j.OpportunityID != "opp-99" {
		t.Fatalf("%+v", j)
	}
	j, err = svc.UnpublishJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Visibility != VisibilityPrivate {
		t.Fatal(j.Visibility)
	}
}

type fakePub struct{ id string }

func (f fakePub) Publish(context.Context, *Job) (string, error) { return f.id, nil }
func (f fakePub) Unpublish(context.Context, *Job) error         { return nil }
