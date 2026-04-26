package deal

import (
	"context"
	"encoding/json"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	"github.com/stawi-opportunities/opportunities/pkg/searchindex"
)

type Matcher struct{}

func New() *Matcher           { return &Matcher{} }
func (*Matcher) Kind() string { return "deal" }

func (*Matcher) SearchFilter(prefs json.RawMessage) (any, error) {
	return searchindex.Filter{Kind: "deal"}, nil
}

func (*Matcher) Score(ctx context.Context, prefs json.RawMessage, opp any) (float64, error) {
	return 0.5, nil
}

var _ matchers.Matcher = (*Matcher)(nil)
