package recipe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldExtractor_Prefix(t *testing.T) {
	// record source with prefix builds a full URL from a bare slug.
	pc, err := NewPageContext("https://api.x/jobs", "", map[string]any{"slug": "abc-senior-dev"})
	require.NoError(t, err)
	fx := FieldExtractor{From: []string{"record"}, JSONPath: "$.slug", Prefix: "https://x.io/job/"}
	out, err := Evaluate(fx, pc)
	require.NoError(t, err)
	assert.Equal(t, "https://x.io/job/abc-senior-dev", out)
}

// headerCapFetcher records whether GetH was used and with which headers.
type headerCapFetcher struct {
	body       []byte
	gotHeaders map[string]string
	usedGetH   bool
}

func (f *headerCapFetcher) Get(_ context.Context, _ string) ([]byte, int, error) {
	return f.body, 200, nil
}
func (f *headerCapFetcher) GetH(_ context.Context, _ string, h map[string]string) ([]byte, int, error) {
	f.usedGetH = true
	f.gotHeaders = h
	return f.body, 200, nil
}

func TestExecutor_SendsListHeaders(t *testing.T) {
	f := &headerCapFetcher{body: []byte(`{"data":[]}`)}
	r := &Recipe{
		Acquisition: "api",
		List:        ListRule{Mode: "api", Endpoint: "https://api.x/jobs", ItemsPath: "$.data", Headers: map[string]string{"x-custom-header": "tok"}, Pagination: Pagination{Mode: "none"}},
		Detail:      DetailRule{RecordSource: "api"},
	}
	e := NewExecutor(r, f)
	_, _, _, _, _ = e.apiPage(context.Background(), jobSource(), "https://api.x/jobs", "")
	assert.True(t, f.usedGetH, "executor should use GetH when recipe declares headers")
	assert.Equal(t, "tok", f.gotHeaders["x-custom-header"])
}
