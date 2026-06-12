package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_RoundTrip(t *testing.T) {
	cases := []PageState{
		{},
		{page: 7},
		{cursor: "abc123"},
		{url: "https://x.io/jobs?p=3", page: 3, cursor: "tok"},
	}
	for _, want := range cases {
		raw, err := MarshalState(want)
		require.NoError(t, err)
		got, err := UnmarshalState(raw)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestState_UnmarshalEmptyIsZero(t *testing.T) {
	got, err := UnmarshalState(nil)
	require.NoError(t, err)
	assert.Equal(t, PageState{}, got)
}

func TestStateProgress(t *testing.T) {
	page, url := StateProgress(PageState{url: "https://x.io/p", page: 4, cursor: "c"})
	assert.Equal(t, 4, page)
	assert.Equal(t, "https://x.io/p", url)
}
