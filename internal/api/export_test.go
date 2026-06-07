package api

import (
	"testing"

	ptymanager "github.com/raziel-ai/raziel/internal/pty"
)

// Test-only seams (compiled only under `go test`). They expose the attach-auth
// decision so it can be unit-tested without standing up a full server or driving
// a real WebSocket upgrade.

// NewTestServer returns a minimal *Server sufficient for attach-auth tests. It
// defaults ptyAuth to a local token store, matching production New().
func NewTestServer(_ *testing.T) *Server {
	return &Server{ptyAuth: newWsTokenStore()}
}

// SetPtyAuthorizer swaps the attach-authorization backing (the seam) for a fake.
func (s *Server) SetPtyAuthorizer(a ptymanager.PtyAuthorizer) { s.ptyAuth = a }

// AuthorizeAttachForTest exposes authorizeAttach. The returned error mirrors the
// unexported attachError's status so external tests can assert it.
func (s *Server) AuthorizeAttachForTest(token, sandboxID string) (ptymanager.AttachGrant, *AttachErrorForTest) {
	grant, aerr := s.authorizeAttach(token, sandboxID)
	if aerr != nil {
		return grant, &AttachErrorForTest{Status: aerr.Status, Msg: aerr.Msg}
	}
	return grant, nil
}

// AttachErrorForTest is the test-visible view of an attach authorization failure.
type AttachErrorForTest struct {
	Status int
	Msg    string
}
