package auth_test

import (
	"testing"

	"github.com/raziel-ai/raziel/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestHasScope(t *testing.T) {
	adminCtx := auth.Context{
		Scopes: []auth.Scope{auth.ScopeAdmin},
	}
	assert.True(t, adminCtx.HasScope(auth.ScopeRead))
	assert.True(t, adminCtx.HasScope(auth.ScopeDeploy))
	assert.True(t, adminCtx.HasScope(auth.ScopeDelete))
	assert.True(t, adminCtx.HasScope(auth.ScopeAdmin))

	readCtx := auth.Context{
		Scopes: []auth.Scope{auth.ScopeRead},
	}
	assert.True(t, readCtx.HasScope(auth.ScopeRead))
	assert.False(t, readCtx.HasScope(auth.ScopeDeploy))
	assert.False(t, readCtx.HasScope(auth.ScopeDelete))
	assert.False(t, readCtx.HasScope(auth.ScopeAdmin))

	deployCtx := auth.Context{
		Scopes: []auth.Scope{auth.ScopeDeploy},
	}
	assert.True(t, deployCtx.HasScope(auth.ScopeRead))
	assert.True(t, deployCtx.HasScope(auth.ScopeDeploy))
	assert.False(t, deployCtx.HasScope(auth.ScopeDelete))
}
