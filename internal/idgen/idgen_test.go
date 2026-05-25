package idgen_test

import (
	"strings"
	"testing"

	"github.com/raziel-ai/raziel/internal/idgen"
	"github.com/stretchr/testify/assert"
)

func TestDeployment(t *testing.T) {
	id := idgen.Deployment()
	assert.True(t, strings.HasPrefix(id, "dep_"), "got %s", id)
	assert.Len(t, id, 3+1+16) // "dep" + "_" + 16 hex chars
}

func TestUniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := idgen.Deployment()
		assert.False(t, seen[id], "collision at %d: %s", i, id)
		seen[id] = true
	}
}
