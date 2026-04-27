package matchers_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/apps/matching/service/matchers"
	dealm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/deal"
	fundingm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/funding"
	jobm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/job"
	scholarshipm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/scholarship"
	tenderm "github.com/stawi-opportunities/opportunities/apps/matching/service/matchers/tender"
)

func TestAllFiveMatchersRegister(t *testing.T) {
	r := matchers.NewRegistry()
	r.Register(jobm.New())
	r.Register(scholarshipm.New())
	r.Register(tenderm.New())
	r.Register(dealm.New())
	r.Register(fundingm.New())
	want := []string{"deal", "funding", "job", "scholarship", "tender"}
	got := r.Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() len = %d, want %d (got %v)", len(got), len(want), got)
	}
	// Registry.Kinds() iterates a map so order is non-deterministic;
	// check for set membership instead.
	for _, k := range want {
		if _, ok := r.For(k); !ok {
			t.Errorf("missing matcher for %q", k)
		}
	}
}
