package controlplane_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	// issued tracks server-issued challenges and whether they've been consumed,
	// modelling the control plane's single-use challenge store.
	issued       map[string]bool
	challengeNum int
}

func newFakeControlPlane(t *testing.T) *fakeControlPlane {
	t.Helper()
	cp := &fakeControlPlane{hostID: "host-xyz", issued: map[string]bool{}}
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

	// Step 1: issue a fresh server-side challenge bound to the host (single-use).
	mux.HandleFunc("/api/internal/heartbeat/challenge", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ComputeHostID string `json:"computeHostId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ComputeHostID != cp.hostID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cp.challengeNum++
		ch := fmt.Sprintf("server-challenge-%d", cp.challengeNum)
		cp.issued[ch] = false // issued, not yet consumed
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "challenge": ch})
	})

	// Step 2: verify the signed challenge — it must be one WE issued, unused, and
	// signed by the enrolled key. Server-issued + single-use ⇒ un-replayable.
	mux.HandleFunc("/api/internal/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ComputeHostID string `json:"computeHostId"`
			Challenge     string `json:"challenge"`
			Signature     string `json:"signature"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		consumed, issued := cp.issued[body.Challenge]
		if !issued || consumed { // unknown or replayed challenge
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sig, _ := base64.StdEncoding.DecodeString(body.Signature)
		cp.lastSigOK = cp.enrolledPub != nil &&
			ed25519.Verify(cp.enrolledPub, []byte(body.Challenge), sig)
		if !cp.lastSigOK || body.ComputeHostID != cp.hostID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		cp.issued[body.Challenge] = true // consume (single-use)
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

	// Heartbeat: fetch a server-issued challenge, sign it with the KEYPAIR.
	require.NoError(t, client.Heartbeat())
	assert.Equal(t, 1, cp.heartbeats)
	assert.True(t, cp.lastSigOK, "heartbeat was signed by the enrolled key")
	assert.Equal(t, 1, cp.challengeNum, "box requested a server-issued challenge")
}

func TestClient_EachHeartbeatUsesAFreshServerChallenge(t *testing.T) {
	cp := newFakeControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	client := controlplane.NewClient(cp.server.URL, ks)
	_, err = client.Enroll("CODE")
	require.NoError(t, err)

	require.NoError(t, client.Heartbeat())
	require.NoError(t, client.Heartbeat())
	assert.Equal(t, 2, cp.heartbeats)
	assert.Equal(t, 2, cp.challengeNum, "each heartbeat fetched its OWN server challenge")
}

func TestClient_ReplayedHeartbeatRejected(t *testing.T) {
	// A captured (challenge, signature) pair can't be replayed: the control plane
	// consumes the challenge on first use, so re-posting it fails. This is the
	// whole point of server-issued single-use challenges.
	cp := newFakeControlPlane(t)
	ks, err := controlplane.LoadOrCreateKeystore(t.TempDir())
	require.NoError(t, err)
	client := controlplane.NewClient(cp.server.URL, ks)
	_, err = client.Enroll("CODE")
	require.NoError(t, err)

	require.NoError(t, client.Heartbeat())
	// Replay the exact challenge the box just used, re-signed identically.
	usedChallenge := fmt.Sprintf("server-challenge-%d", cp.challengeNum)
	sig := base64.StdEncoding.EncodeToString(ks.Sign([]byte(usedChallenge)))
	body, _ := json.Marshal(map[string]string{
		"computeHostId": cp.hostID, "challenge": usedChallenge, "signature": sig,
	})
	resp, err := http.Post(cp.server.URL+"/api/internal/heartbeat", "application/json",
		bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "replayed challenge rejected")
	assert.Equal(t, 1, cp.heartbeats, "replay did not count as a new heartbeat")
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
