package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsKnownSourceType_EnginesOnly(t *testing.T) {
	for _, e := range KnownEngineTypes {
		assert.True(t, IsKnownSourceType(e), e)
	}
	// Site names are not engines — they must not be accepted as types.
	assert.False(t, IsKnownSourceType("remoteok"))
	assert.False(t, IsKnownSourceType("jobberman"))
	assert.False(t, IsKnownSourceType("brightermonday"))
	assert.False(t, IsKnownSourceType("greenhouse"))
	assert.False(t, IsKnownSourceType(""))
}

func TestRequiresRecipe(t *testing.T) {
	assert.True(t, RequiresRecipe(SourceAPI))
	assert.True(t, RequiresRecipe(SourceGenericHTML))
	assert.False(t, RequiresRecipe(SourceSchemaOrg))
	assert.False(t, RequiresRecipe(SourceSitemap))
	assert.False(t, RequiresRecipe(SourceWorkday))
	assert.False(t, RequiresRecipe(SourceSmartRecruitersAPI))
}
