package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
)

type admitterFunc func(context.Context, string, int) (int, time.Duration)

func (f admitterFunc) Admit(ctx context.Context, topic string, want int) (int, time.Duration) {
	return f(ctx, topic, want)
}

type fakeSourceGetter struct{ rows map[string]*domain.Source }

func (g *fakeSourceGetter) GetByID(_ context.Context, id string) (*domain.Source, error) {
	s := g.rows[id]
	if s == nil {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

type envCollector[P any] struct {
	topic string
	mu    sync.Mutex
	got   []eventsv1.Envelope[P]
}

func (c *envCollector[P]) Name() string                        { return c.topic }
func (c *envCollector[P]) PayloadType() any                    { var raw json.RawMessage; return &raw }
func (c *envCollector[P]) Validate(context.Context, any) error { return nil }
func (c *envCollector[P]) Execute(_ context.Context, payload any) error {
	var env eventsv1.Envelope[P]
	if err := json.Unmarshal(*payload.(*json.RawMessage), &env); err != nil {
		return err
	}
	c.mu.Lock()
	c.got = append(c.got, env)
	c.mu.Unlock()
	return nil
}
func (c *envCollector[P]) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.got) }
func (c *envCollector[P]) Snapshot() []eventsv1.Envelope[P] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventsv1.Envelope[P](nil), c.got...)
}
