package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/pitabwire/frame"
	"github.com/pitabwire/frame/events"
	"github.com/pitabwire/frame/frametests"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/content"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// --- fakes ---

type fakeConnector struct {
	jobs []domain.ExternalOpportunity
	raw  []byte
}

func (f *fakeConnector) Type() domain.SourceType { return domain.SourceGenericHTML }
func (f *fakeConnector) Crawl(_ context.Context, _ domain.Source) connectors.CrawlIterator {
	return connectors.NewSinglePageIterator(f.jobs, f.raw, 200, nil)
}

// fakeSourceGetter satisfies the handler's GetByID dependency.
type fakeSourceGetter struct {
	rows map[string]*domain.Source
}

func (g *fakeSourceGetter) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s, ok := g.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

// envCollector captures envelopes for topic assertions. Race-safe.
type envCollector[P any] struct {
	topic string
	mu    sync.Mutex
	got   []eventsv1.Envelope[P]
}

func (c *envCollector[P]) Name() string { return c.topic }
func (c *envCollector[P]) PayloadType() any { var raw json.RawMessage; return &raw }
func (c *envCollector[P]) Validate(context.Context, any) error { return nil }
func (c *envCollector[P]) Execute(_ context.Context, payload any) error {
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[P]
	if err := json.Unmarshal(*raw, &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *envCollector[P]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}
func (c *envCollector[P]) Snapshot() []eventsv1.Envelope[P] {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]eventsv1.Envelope[P], len(c.got))
	copy(out, c.got)
	return out
}

// --- test ---

func TestCrawlRequestHandlerEmitsVariantAndPageCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-test"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	for _, c := range []events.EventI{variantCol, pageCol} {
		svc.EventsManager().Add(c)
	}

	// Start Frame so subscriptions are live before Execute runs.
	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	reg := connectors.NewRegistry()
	reg.Register(&fakeConnector{
		jobs: []domain.ExternalOpportunity{{
			Kind:          "job",
			ExternalID:    "ext-1",
			Title:         "Backend Engineer",
			IssuingEntity: "Acme",
			ApplyURL:      "https://acme.example/jobs/ext-1",
			Description:   "We are looking for a skilled backend engineer to join our team and build scalable services.",
		}},
		raw: []byte("<html>job body</html>"),
	})

	srcs := &fakeSourceGetter{
		rows: map[string]*domain.Source{
			"s1": {
				BaseModel: domain.BaseModel{ID: "s1"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://acme.example/jobs",
				Status:    domain.SourceActive,
				Country:   "KE",
				Language:  "en",
			},
		},
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       reg,
		Archive:        archive.NewFakeArchive(),
		Extractor:      nil, // nil extractor → no AI enrichment, deterministic path
		DiscoverSample: 0,   // disable sampling in this test
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-1",
		SourceID:  "s1",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if variantCol.Len() == 1 && pageCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if variantCol.Len() != 1 {
		t.Fatalf("variant events=%d, want 1", variantCol.Len())
	}
	if pageCol.Len() != 1 {
		t.Fatalf("page-completed events=%d, want 1", pageCol.Len())
	}

	v := variantCol.Snapshot()[0].Payload
	if v.VariantID == "" || v.HardKey == "" {
		t.Fatalf("variant missing ids: %+v", v)
	}
	if v.SourceID != "s1" || v.Title != "Backend Engineer" {
		t.Fatalf("variant content lost: %+v", v)
	}
	rawRef, _ := v.Attributes["raw_archive_ref"].(string)
	if rawRef == "" {
		t.Fatalf("variant missing raw_archive_ref attribute")
	}

	pc := pageCol.Snapshot()[0].Payload
	if pc.SourceID != "s1" || pc.RequestID != "req-1" {
		t.Fatalf("page-completed ids wrong: %+v", pc)
	}
	if pc.JobsFound != 1 || pc.JobsEmitted != 1 || pc.JobsRejected != 0 {
		t.Fatalf("page-completed counts wrong: %+v", pc)
	}
}

func TestCrawlRequestHandlerUnknownSourceEmitsErrorCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-unknown"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	svc.EventsManager().Add(pageCol)

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:      svc,
		Sources:  &fakeSourceGetter{rows: map[string]*domain.Source{}},
		Registry: connectors.NewRegistry(),
		Archive:  archive.NewFakeArchive(),
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-unknown",
		SourceID:  "missing",
		Mode:      "auto",
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("unexpected Execute err: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pageCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pageCol.Len() != 1 {
		t.Fatalf("expected one page-completed event, got %d", pageCol.Len())
	}
	if got := pageCol.Snapshot()[0].Payload.ErrorCode; got != "source_not_found" {
		t.Fatalf("error_code=%q, want source_not_found", got)
	}

	_ = content.Extracted{} // keep content import alive for fakeConnector file
}

// TestCrawlRequestHandler_ForwardsKindSpecificAttributes verifies that
// kind-specific Attributes from the connector (e.g. field_of_study,
// degree_level on a scholarship) survive normalize and ride out on the
// emitted VariantIngestedV1. Regression for Bug #4 of the
// production-readiness audit.
func TestCrawlRequestHandler_ForwardsKindSpecificAttributes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-attrs"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	for _, c := range []events.EventI{variantCol, pageCol} {
		svc.EventsManager().Add(c)
	}

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	deadline := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	connReg := connectors.NewRegistry()
	connReg.Register(&fakeConnector{
		jobs: []domain.ExternalOpportunity{{
			Kind:          "scholarship",
			ExternalID:    "sch-1",
			Title:         "MSc Climate Science",
			IssuingEntity: "ETH Zurich",
			ApplyURL:      "https://example.com/apply",
			Description:   "Long description that is well above the minimum length required for verify to be happy.",
			Deadline:      &deadline,
			Attributes: map[string]any{
				"field_of_study": "Climate",
				"degree_level":   "masters",
			},
		}},
		raw: []byte("<html>scholarship body</html>"),
	})

	srcs := &fakeSourceGetter{
		rows: map[string]*domain.Source{
			"sch-source": {
				BaseModel: domain.BaseModel{ID: "sch-source"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://example.com/scholarships",
				Status:    domain.SourceActive,
				Country:   "CH",
				Language:  "en",
				Kinds:     pq.StringArray{"scholarship"},
			},
		},
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       connReg,
		Kinds:          reg,
		Archive:        archive.NewFakeArchive(),
		DiscoverSample: 0,
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-attrs",
		SourceID:  "sch-source",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wait := time.Now().Add(3 * time.Second)
	for time.Now().Before(wait) {
		if variantCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if variantCol.Len() != 1 {
		t.Fatalf("variant events=%d, want 1 (was the record rejected?)", variantCol.Len())
	}

	v := variantCol.Snapshot()[0].Payload
	if v.Kind != "scholarship" {
		t.Fatalf("variant Kind=%q, want scholarship", v.Kind)
	}
	if got, _ := v.Attributes["field_of_study"].(string); got != "Climate" {
		t.Fatalf("Attributes[field_of_study]=%q, want Climate", got)
	}
	if got, _ := v.Attributes["degree_level"].(string); got != "masters" {
		t.Fatalf("Attributes[degree_level]=%q, want masters", got)
	}
}

// TestCrawlRequestHandler_EmptyKindsDefaultsToJob verifies that a Source
// constructed with empty Kinds (in-code path; the DB column itself
// defaults to '{job}') no longer rejects every record with a confusing
// "kind \"job\" not declared by source (declared: [])" message — the
// handler defaults to ["job"] before calling Verify. Regression for
// Bug #5 of the production-readiness audit.
func TestCrawlRequestHandler_EmptyKindsDefaultsToJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithName("crawl-req-empty-kinds"),
		frametests.WithNoopDriver(),
	)
	defer svc.Stop(ctx)

	variantCol := &envCollector[eventsv1.VariantIngestedV1]{topic: eventsv1.TopicVariantsIngested}
	pageCol := &envCollector[eventsv1.CrawlPageCompletedV1]{topic: eventsv1.TopicCrawlPageCompleted}
	for _, c := range []events.EventI{variantCol, pageCol} {
		svc.EventsManager().Add(c)
	}

	go func() { _ = svc.Run(ctx, "") }()
	time.Sleep(200 * time.Millisecond)

	reg, err := opportunity.LoadFromDir("../../../definitions/opportunity-kinds")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	connReg := connectors.NewRegistry()
	connReg.Register(&fakeConnector{
		jobs: []domain.ExternalOpportunity{{
			Kind:           "job",
			ExternalID:     "ext-empty",
			Title:          "Backend Engineer",
			IssuingEntity:  "Acme",
			ApplyURL:       "https://acme.example/jobs/ext-empty",
			Description:    "We are looking for a skilled backend engineer to join our team and build scalable services.",
			AnchorLocation: &domain.Location{Country: "KE"},
			Attributes: map[string]any{
				"employment_type": "full-time",
			},
		}},
		raw: []byte("<html>job body</html>"),
	})

	srcs := &fakeSourceGetter{
		rows: map[string]*domain.Source{
			"s-empty": {
				BaseModel: domain.BaseModel{ID: "s-empty"},
				Type:      domain.SourceGenericHTML,
				BaseURL:   "https://acme.example/jobs",
				Status:    domain.SourceActive,
				Country:   "KE",
				Language:  "en",
				// Kinds intentionally left nil/empty — that's the
				// regression under test; the handler must default it
				// to ["job"] before calling Verify.
			},
		},
	}

	h := NewCrawlRequestHandler(CrawlRequestDeps{
		Svc:            svc,
		Sources:        srcs,
		Registry:       connReg,
		Kinds:          reg,
		Archive:        archive.NewFakeArchive(),
		DiscoverSample: 0,
	})

	env := eventsv1.NewEnvelope(eventsv1.TopicCrawlRequests, eventsv1.CrawlRequestV1{
		RequestID: "req-empty-kinds",
		SourceID:  "s-empty",
		Mode:      "auto",
		Attempt:   1,
	})
	raw, _ := json.Marshal(env)
	rm := json.RawMessage(raw)
	if err := h.Execute(ctx, &rm); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wait := time.Now().Add(3 * time.Second)
	for time.Now().Before(wait) {
		if variantCol.Len() == 1 && pageCol.Len() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if variantCol.Len() != 1 {
		t.Fatalf("variant events=%d, want 1 (record rejected by Verify?)", variantCol.Len())
	}
	pc := pageCol.Snapshot()[0].Payload
	if pc.JobsRejected != 0 {
		t.Fatalf("JobsRejected=%d, want 0 — empty Kinds should default to [job]", pc.JobsRejected)
	}
}
