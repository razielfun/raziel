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
	return pty.NewControlPlaneAuthorizer(c)
}

// Slice 3 (#81): a PTY attach is gated by a PtyAuthorizer seam, so the transport
// (inbound WS today; the dial-out reverse tunnel at #86) is decoupled from HOW an
// attach is authorized. Two backings:
//   - local token store (the existing inbound-WS behavior), and
//   - the control plane (#79 host token, verified server-side).
// The WS handler asks the seam "may this token attach?"; it never knows which.

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
	auth := pty.NewControlPlaneAuthorizer(v)

	userID, err := auth.Authorize("good-token")
	require.NoError(t, err)
	assert.Equal(t, "u1", userID)
	assert.Equal(t, "good-token", v.gotTok, "the seam forwards the token to the control plane")
	assert.Equal(t, 1, v.calls)
}

func TestControlPlaneAuthorizer_DeniesWhenVerifierRejects(t *testing.T) {
	v := &fakeVerifier{err: errors.New("pty attach refused (401): invalid token")}
	auth := pty.NewControlPlaneAuthorizer(v)

	_, err := auth.Authorize("forged")
	require.Error(t, err, "a control-plane rejection denies the attach")
}

func TestLocalAuthorizer_AllowsAKnownSingleUseToken(t *testing.T) {
	// The local seam mirrors the existing inbound-WS token store: a registered
	// token authorizes exactly one attach.
	store := pty.NewLocalAuthorizer()
	store.Register("tok-1", "user-7")

	userID, err := store.Authorize("tok-1")
	require.NoError(t, err)
	assert.Equal(t, "user-7", userID)
}

func TestLocalAuthorizer_RejectsUnknownToken(t *testing.T) {
	store := pty.NewLocalAuthorizer()
	_, err := store.Authorize("never-registered")
	require.Error(t, err)
}

func TestLocalAuthorizer_TokenIsSingleUse(t *testing.T) {
	store := pty.NewLocalAuthorizer()
	store.Register("tok-1", "user-7")
	_, err := store.Authorize("tok-1")
	require.NoError(t, err)
	_, err = store.Authorize("tok-1")
	require.Error(t, err, "a token authorizes exactly one attach (single-use)")
}

// Both implementations satisfy the same seam, so the transport layer depends on
// the interface, not a concrete backing.
func TestBothBackingsSatisfyTheSeam(t *testing.T) {
	var _ pty.PtyAuthorizer = pty.NewControlPlaneAuthorizer(&fakeVerifier{})
	var _ pty.PtyAuthorizer = pty.NewLocalAuthorizer()
}
