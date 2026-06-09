package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyTransforms(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		names   []string
		base    string
		want    string
		wantErr bool
	}{
		{name: "trim", in: "  hi  ", names: []string{"trim"}, want: "hi"},
		{name: "lower", in: "HeLLo", names: []string{"lower"}, want: "hello"},
		{name: "collapse_ws", in: "a   b\n\tc", names: []string{"collapse_ws"}, want: "a b c"},
		{name: "html_to_text", in: "<p>Hi <b>there</b></p>", names: []string{"html_to_text"}, want: "Hi there"},
		{name: "chain trim+lower", in: "  YO ", names: []string{"trim", "lower"}, want: "yo"},
		{name: "absolute_url relative", in: "/jobs/1", names: []string{"absolute_url"}, base: "https://x.io/list", want: "https://x.io/jobs/1"},
		{name: "absolute_url already-absolute", in: "https://y.io/a", names: []string{"absolute_url"}, base: "https://x.io/list", want: "https://y.io/a"},
		{name: "parse_money", in: "USD 1,250.50", names: []string{"parse_money"}, want: "1250.50"},
		{name: "parse_date iso", in: "2026-06-09T10:00:00Z", names: []string{"parse_date"}, want: "2026-06-09T10:00:00Z"},
		{name: "parse_date ymd", in: "2026-06-09", names: []string{"parse_date"}, want: "2026-06-09T00:00:00Z"},
		{name: "unknown transform errors", in: "x", names: []string{"nope"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyTransforms(tc.in, tc.names, transformCtx{BaseURL: tc.base})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTransformExists(t *testing.T) {
	assert.True(t, transformExists("trim"))
	assert.False(t, transformExists("frobnicate"))
}
