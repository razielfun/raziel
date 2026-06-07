package controlplane_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice 3 (#81): a PTY attach is authorized PER-ATTACH by a #79 host token. The
// box redeems the token against the control plane's pty-attach-verify endpoint
// (NOT a box-local decision); the control plane re-verifies the token's audience
// (this box + pty-attach action — P0 #2) and single-use nonce (P0 #3) and returns
// the authorized userId. A bad / cross-host token is refused. The transport that
// then carries the PTY is governed by a seam (default = inbound WS; the dial-out
// reverse tunnel is #86) — out of scope here; this slice is the AUTHORIZATION.

// fakePtyControlPlane is a stand-in raziel-web that only knows the host id it
// "enrolled" and which token string it considers valid for that host.
type fakePtyControlPlane struct {
	server     *httptest.Server
	hostID     string
	validToken string
	revoked    bool
	calls      int
}

func newFakePtyControlPlane(t *testing.T) *fakePtyControlPlane {
	t.Helper()
	cp := &fakePtyControlPlane{hostID: "host-xyz", validToken: "good-token"}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/internal/pty-attach-verify", func(w http.ResponseWriter, r *http.Request) {
		cp.calls++
		var body struct {
			ComputeHostID string `json:"computeHostId"`
			Token         string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		// Revoked is terminal and checked FIRST (D-S2).
		if cp.revoked {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "host is revoked"})
			return
		}
		// Audience: the token must be this box's, and the one we issued.
		if body.ComputeHostID != cp.hostID || body.Token != cp.validToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "userId": "u1", "action": "pty-attach"})
	})

	cp.server = httptest.NewServer(mux)
	t.Cleanup(cp.server.Close)
	return cp
}

func ptyClient(t *testing.T, url string) *controlplane.Client {
	t.Helper()
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	c := controlplane.NewClient(url, ks)
	c.SetHostID("host-xyz") // already enrolled
	return c
}

func TestClient_VerifyPtyAttach_AcceptsValidToken(t *testing.T) {
	cp := newFakePtyControlPlane(t)
	c := ptyClient(t, cp.server.URL)

	userID, err := c.VerifyPtyAttach("good-token")
	require.NoError(t, err)
	assert.Equal(t, "u1", userID, "control plane returned the authorized user")
	assert.Equal(t, 1, cp.calls)
}

func TestClient_VerifyPtyAttach_RefusesBadToken(t *testing.T) {
	cp := newFakePtyControlPlane(t)
	c := ptyClient(t, cp.server.URL)

	_, err := c.VerifyPtyAttach("forged-token")
	require.Error(t, err, "a token the control plane rejects must not authorize an attach")
}

func TestClient_VerifyPtyAttach_RefusesCrossHostToken(t *testing.T) {
	// A token minted for another box: this client presents its OWN host id, so
	// the control plane's audience check fails (P0 #2). Model it by pointing the
	// client at a different host id than the one the token is valid for.
	cp := newFakePtyControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	c := controlplane.NewClient(cp.server.URL, ks)
	c.SetHostID("host-OTHER") // not the host the token was issued for

	_, err = c.VerifyPtyAttach("good-token")
	require.Error(t, err, "another box's host id must fail the audience check")
}

func TestClient_VerifyPtyAttach_RefusesWhenRevoked(t *testing.T) {
	cp := newFakePtyControlPlane(t)
	cp.revoked = true
	c := ptyClient(t, cp.server.URL)

	_, err := c.VerifyPtyAttach("good-token")
	require.Error(t, err, "a revoked host can never attach (D-S2)")
}

func TestClient_VerifyPtyAttach_FailsBeforeEnroll(t *testing.T) {
	cp := newFakePtyControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	c := controlplane.NewClient(cp.server.URL, ks) // no host id set

	_, err = c.VerifyPtyAttach("good-token")
	require.Error(t, err, "cannot authorize an attach before enrollment")
	assert.Equal(t, 0, cp.calls, "no request made without a host identity")
}
