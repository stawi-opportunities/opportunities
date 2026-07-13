package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsKnownSourceType_EnginesOnly(t *testing.T) {
	for _, e := range KnownEngineTypes {
		assert.True(t, IsKnownSourceType(e), e)
	}
	assert.False(t, IsKnownSourceType("remoteok"))
	assert.False(t, IsKnownSourceType("jobberman"))
	assert.False(t, IsKnownSourceType("brightermonday"))
	assert.False(t, IsKnownSourceType("greenhouse"))
	assert.False(t, IsKnownSourceType(""))
}

func TestRemapLegacySourceType(t *testing.T) {
	// Engines are not remapped (ok=false).
	eng, ok := RemapLegacySourceType(SourceAPI)
	assert.Equal(t, SourceAPI, eng)
	assert.False(t, ok)

	// Historical site types map to engines.
	cases := map[SourceType]SourceType{
		"remoteok":             SourceAPI,
		"arbeitnow":            SourceAPI,
		"brightermonday":       SourceSchemaOrg,
		"jobberman":            SourceSchemaOrg,
		"myjobmag":             SourceSchemaOrg,
		"careers24":            SourceSchemaOrg,
		"greenhouse":           SourceGenericHTML,
		"lever":                SourceGenericHTML,
		"smartrecruiters_page": SourceSmartRecruitersAPI,
	}
	for old, want := range cases {
		got, ok := RemapLegacySourceType(old)
		assert.True(t, ok, old)
		assert.Equal(t, want, got, old)
	}

	// Unknown stays unmapped.
	_, ok = RemapLegacySourceType("not_a_real_board")
	assert.False(t, ok)
}
