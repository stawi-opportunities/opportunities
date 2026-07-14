package crawlaccept

import (
	"context"
	"errors"
	"strings"
	"testing"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type fakePeeler struct {
	clean, how string
	err        error
	calls      int
}

func (f *fakePeeler) PeelHowToApply(_ context.Context, _, _, _ string) (string, string, error) {
	f.calls++
	return f.clean, f.how, f.err
}

func TestPeelAccepted_SetsHowToApply(t *testing.T) {
	body := strings.Repeat("Public role details go here. ", 8)
	p := &eventsv1.VariantIngestedV1{
		Title: "Eng", Kind: "job",
		Attributes: map[string]any{"description": body + "\nEmail apply@x.test"},
	}
	fp := &fakePeeler{clean: "Public only", how: "Email apply@x.test"}
	if err := PeelAccepted(context.Background(), p, fp); err != nil {
		t.Fatal(err)
	}
	if p.HowToApply != "Email apply@x.test" {
		t.Fatalf("HowToApply=%q", p.HowToApply)
	}
	if p.Attributes["description"] != "Public only" {
		t.Fatalf("desc=%v", p.Attributes["description"])
	}
	if fp.calls != 1 {
		t.Fatalf("calls=%d", fp.calls)
	}
}

func TestPeelAccepted_SkipsWhenAlreadySet(t *testing.T) {
	p := &eventsv1.VariantIngestedV1{
		HowToApply: "already",
		Attributes: map[string]any{"description": strings.Repeat("x", 100)},
	}
	fp := &fakePeeler{clean: "n", how: "y"}
	_ = PeelAccepted(context.Background(), p, fp)
	if fp.calls != 0 {
		t.Fatal("should not peel when HowToApply set")
	}
	if p.HowToApply != "already" {
		t.Fatal(p.HowToApply)
	}
}

func TestPeelAccepted_FailOpen(t *testing.T) {
	body := strings.Repeat("Public role details go here. ", 8)
	p := &eventsv1.VariantIngestedV1{
		Attributes: map[string]any{"description": body},
	}
	fp := &fakePeeler{err: errors.New("llm down")}
	err := PeelAccepted(context.Background(), p, fp)
	if err == nil {
		t.Fatal("expected error from peeler")
	}
	// Caller decides fail-open; payload unchanged.
	if p.HowToApply != "" {
		t.Fatal(p.HowToApply)
	}
}
