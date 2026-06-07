package controlplane_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/raziel-ai/raziel/internal/controlplane"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slice 4: the box DIALS OUT — enrolls a one-time code against the control plane,
// then authenticates with its KEYPAIR (signed challenge) to emit heartbeats. This
// is the net-new piece of #80: the prototype was inbound-API-only.

// fakeControlPlane is a stand-in raziel-web: it records the enrolled pubkey and
// verifies heartbeat signatures against it (proving post-enroll auth = keypair,
// not the code — threat-model D-S1).
type fakeControlPlane struct {
	server      *httptest.Server
	enrolledPub ed25519.PublicKey
	hostID      string
	heartbeats  int
	lastSigOK   bool
}

func newFakeControlPlane(t *testing.T) *fakeControlPlane {
	t.Helper()
	cp := &fakeControlPlane{hostID: "host-xyz"}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/internal/enroll", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Code, PublicKey string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Code == "" || body.PublicKey == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pub, err := parseSSHEd25519(body.PublicKey)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		cp.enrolledPub = pub
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "computeHostId": cp.hostID, "status": "online",
		})
	})

	mux.HandleFunc("/api/internal/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ComputeHostID string `json:"computeHostId"`
			Challenge     string `json:"challenge"`
			Signature     string `json:"signature"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sig, _ := base64.StdEncoding.DecodeString(body.Signature)
		// The control plane verifies the heartbeat against the ENROLLED pubkey.
		cp.lastSigOK = cp.enrolledPub != nil &&
			ed25519.Verify(cp.enrolledPub, []byte(body.Challenge), sig)
		if !cp.lastSigOK || body.ComputeHostID != cp.hostID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		cp.heartbeats++
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	cp.server = httptest.NewServer(mux)
	t.Cleanup(cp.server.Close)
	return cp
}

func TestClient_EnrollThenHeartbeat(t *testing.T) {
	cp := newFakeControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)

	client := controlplane.NewClient(cp.server.URL, ks)

	// Enroll: dial out, redeem the code, bind our on-box pubkey.
	hostID, err := client.Enroll("ENROLL-CODE-123")
	require.NoError(t, err)
	assert.Equal(t, "host-xyz", hostID)
	assert.Equal(t, ks.PublicKey(), cp.enrolledPub, "control plane bound OUR pubkey")

	// Heartbeat: authenticate with the KEYPAIR (not the code).
	require.NoError(t, client.Heartbeat())
	assert.Equal(t, 1, cp.heartbeats)
	assert.True(t, cp.lastSigOK, "heartbeat was signed by the enrolled key")
}

func TestClient_HeartbeatAfterRestoredHostID(t *testing.T) {
	// On agentd restart the box reloads its keystore + persisted host id and
	// heartbeats WITHOUT re-enrolling (the code is long gone). Simulate that:
	// enroll once to register the pubkey, then a fresh client restores the id.
	cp := newFakeControlPlane(t)
	dir := t.TempDir()
	ks, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)
	enroller := controlplane.NewClient(cp.server.URL, ks)
	hostID, err := enroller.Enroll("CODE")
	require.NoError(t, err)

	// Restart: same on-box keystore, host id restored from disk, no enroll.
	ks2, err := controlplane.LoadOrCreateKeystore(dir)
	require.NoError(t, err)
	restored := controlplane.NewClient(cp.server.URL, ks2)
	restored.SetHostID(hostID)
	require.NoError(t, restored.Heartbeat())
	assert.True(t, cp.lastSigOK)
}

func TestClient_HeartbeatBeforeEnrollFails(t *testing.T) {
	cp := newFakeControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	client := controlplane.NewClient(cp.server.URL, ks)

	// No hostID yet → the client can't heartbeat.
	err = client.Heartbeat()
	require.Error(t, err)
	assert.Equal(t, 0, cp.heartbeats)
}

func TestClient_EnrollRejectedOnBadCode(t *testing.T) {
	cp := newFakeControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	client := controlplane.NewClient(cp.server.URL, ks)

	_, err = client.Enroll("") // control plane 400s a blank code
	require.Error(t, err)
}

// parseSSHEd25519 decodes an "ssh-ed25519 <base64>" line back to a public key —
// the fake control plane's parser, mirroring what raziel-web does on redeem.
func parseSSHEd25519(line string) (ed25519.PublicKey, error) {
	return controlplane.ParseAuthorizedKey(line)
}
