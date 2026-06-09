package service

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stretchr/testify/assert"
)

func TestUseRecipePath(t *testing.T) {
	r := &recipe.Recipe{Acquisition: "api"}
	assert.False(t, useRecipePath(false, r))  // disabled -> connector
	assert.False(t, useRecipePath(true, nil)) // no recipe -> connector
	assert.True(t, useRecipePath(true, r))    // enabled + recipe -> executor
}
