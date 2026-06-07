package api_test

import (
	"testing"

	"github.com/raziel-ai/raziel/internal/api"
	"github.com/raziel-ai/raziel/internal/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #81 wiring: handleSandboxWs authorizes an attach through the PtyAuthorizer seam
// (not wsTokens directly), then binds the grant's Target to the URL sandbox id.
// authorizeAttach is that decision, unit-tested here against a fake authorizer so
// the behavior is covered without a full WebSocket upgrade. Runs on the Linux CI
// (internal/api is a linux-only package).

type fakeAuthorizer struct {
	grant pty.AttachGrant
	err   error
}

func (f fakeAuthorizer) Authorize(string) (pty.AttachGrant, error) { return f.grant, f.err }

func TestAuthorizeAttach_AllowsWhenTargetMatchesSandbox(t *testing.T) {
	s := api.NewTestServer(t)
	s.SetPtyAuthorizer(fakeAuthorizer{grant: pty.AttachGrant{Target: "sbx-1", UserID: "u1"}})

	grant, apiErr := s.AuthorizeAttachForTest("good-token", "sbx-1")
	require.Nil(t, apiErr)
	assert.Equal(t, "sbx-1", grant.Target)
	assert.Equal(t, "u1", grant.UserID)
}

func TestAuthorizeAttach_RejectsWhenAuthorizerDenies(t *testing.T) {
	s := api.NewTestServer(t)
	s.SetPtyAuthorizer(fakeAuthorizer{err: assertAnErr()})

	_, apiErr := s.AuthorizeAttachForTest("forged", "sbx-1")
	require.NotNil(t, apiErr)
	assert.Equal(t, 401, apiErr.Status)
}

func TestAuthorizeAttach_RejectsCrossResourceToken(t *testing.T) {
	// A token good for sbx-2 must not open an attach to sbx-1 (P0 #2, uniformly).
	s := api.NewTestServer(t)
	s.SetPtyAuthorizer(fakeAuthorizer{grant: pty.AttachGrant{Target: "sbx-2", UserID: "u1"}})

	_, apiErr := s.AuthorizeAttachForTest("good-token", "sbx-1")
	require.NotNil(t, apiErr)
	assert.Equal(t, 403, apiErr.Status)
}

func assertAnErr() error { return errAttachDenied{} }

type errAttachDenied struct{}

func (errAttachDenied) Error() string { return "denied" }
