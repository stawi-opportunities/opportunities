package connectors

import (
	"context"

	"stawi.jobs/internal/domain"
)

type Connector interface {
	Type() domain.SourceType
	Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error)
}

type Registry struct {
	items map[domain.SourceType]Connector
}

func NewRegistry(connectors ...Connector) *Registry {
	items := make(map[domain.SourceType]Connector, len(connectors))
	for _, c := range connectors {
		items[c.Type()] = c
	}
	return &Registry{items: items}
}

func (r *Registry) Get(typ domain.SourceType) (Connector, bool) {
	c, ok := r.items[typ]
	return c, ok
}

func (r *Registry) Types() []domain.SourceType {
	out := make([]domain.SourceType, 0, len(r.items))
	for t := range r.items {
		out = append(out, t)
	}
	return out
}
