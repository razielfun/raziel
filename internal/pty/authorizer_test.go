package pty_test

import (
	"errors"
	"testing"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/raziel-ai/raziel/internal/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real control-plane client must back the control-plane authorizer — this is
// the slice-3 wiring contract. If VerifyPtyAttach's signature drifts, this fails
// to compile (NewControlPlaneAuthorizer takes the ptyVerifier the seam expects).
var _ = func() pty.PtyAuthorizer {
	var c *controlplane.Client
	return pty.NewControlPlaneAuthorizer(c, "host-1")
}

// #81: a PTY attach is gated by a PtyAuthorizer seam, so the transport (inbound
// WS today; the dial-out reverse tunnel at #86) is decoupled from HOW an attach
// is authorized. Authorize returns an AttachGrant carrying BOTH the resource the
// token is bound to (Target) and the authorized user (UserID) — so the WS handler
// can bind the token to the URL resource id uniformly, whichever backing answered:
//   - local token store (the existing inbound-WS behavior, Target = sandbox id),
//   - the control plane (#79 host token, Target = host id, verified server-side).

// fakeVerifier stands in for controlplane.Client.VerifyPtyAttach.
type fakeVerifier struct {
	userID string
	err    error
	calls  int
	gotTok string
}

func (f *fakeVerifier) VerifyPtyAttach(token string) (string, error) {
	f.calls++
	f.gotTok = token
	return f.userID, f.err
}

func TestControlPlaneAuthorizer_AllowsWhenVerifierAccepts(t *testing.T) {
	v := &fakeVerifier{userID: "u1"}
	auth := pty.NewControlPlaneAuthorizer(v, "host-1")

	grant, err := auth.Authorize("good-token")
	require.NoError(t, err)
	assert.Equal(t, "u1", grant.UserID)
	assert.Equal(t, "host-1", grant.Target, "control-plane grant binds to the box's host id")
	assert.Equal(t, "good-token", v.gotTok, "the seam forwards the token to the control plane")
	assert.Equal(t, 1, v.calls)
}

func TestControlPlaneAuthorizer_DeniesWhenVerifierRejects(t *testing.T) {
	v := &fakeVerifier{err: errors.New("pty attach refused (401): invalid token")}
	auth := pty.NewControlPlaneAuthorizer(v, "host-1")

	_, err := auth.Authorize("forged")
	require.Error(t, err, "a control-plane rejection denies the attach")
}

func TestLocalAuthorizer_AllowsAKnownSingleUseToken(t *testing.T) {
	// The local seam mirrors the existing inbound-WS token store: a registered
	// token authorizes exactly one attach, bound to its sandbox id.
	store := pty.NewLocalAuthorizer()
	store.Register("tok-1", "sbx-7", "user-7")

	grant, err := store.Authorize("tok-1")
	require.NoError(t, err)
	assert.Equal(t, "sbx-7", grant.Target)
	assert.Equal(t, "user-7", grant.UserID)
}

func TestLocalAuthorizer_RejectsUnknownToken(t *testing.T) {
	store := pty.NewLocalAuthorizer()
	_, err := store.Authorize("never-registered")
	require.Error(t, err)
}

func TestLocalAuthorizer_TokenIsSingleUse(t *testing.T) {
	store := pty.NewLocalAuthorizer()
	store.Register("tok-1", "sbx-7", "user-7")
	_, err := store.Authorize("tok-1")
	require.NoError(t, err)
	_, err = store.Authorize("tok-1")
	require.Error(t, err, "a token authorizes exactly one attach (single-use)")
}

// Both implementations satisfy the same seam, so the transport layer depends on
// the interface, not a concrete backing.
func TestBothBackingsSatisfyTheSeam(t *testing.T) {
	var _ pty.PtyAuthorizer = pty.NewControlPlaneAuthorizer(&fakeVerifier{}, "h")
	var _ pty.PtyAuthorizer = pty.NewLocalAuthorizer()
}
