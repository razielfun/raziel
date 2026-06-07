package controlplane

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is raziel-agentd's outbound connection to the raziel-web control plane.
// It enrolls a one-time code (binding this box's on-box public key to a
// ComputeHost) and thereafter authenticates with the KEYPAIR — every heartbeat
// is signed by the on-box private key, never the enrollment code (D-S1).
type Client struct {
	baseURL string
	ks      *Keystore
	http    *http.Client

	// hostID is the ComputeHost id the control plane assigned at enrollment. Set
	// by Enroll; required by Heartbeat.
	hostID string
}

// NewClient builds a control-plane client for baseURL using the on-box keystore.
func NewClient(baseURL string, ks *Keystore) *Client {
	return &Client{
		baseURL: baseURL,
		ks:      ks,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// HostID returns the enrolled ComputeHost id (empty until Enroll succeeds).
func (c *Client) HostID() string { return c.hostID }

// SetHostID restores a previously-enrolled host id (e.g. read from disk on
// agentd restart) so the client can heartbeat without re-enrolling.
func (c *Client) SetHostID(id string) { c.hostID = id }

type enrollRequest struct {
	Code      string `json:"code"`
	PublicKey string `json:"publicKey"`
}

type enrollResponse struct {
	OK            bool   `json:"ok"`
	ComputeHostID string `json:"computeHostId"`
	Status        string `json:"status"`
	Error         string `json:"error"`
}

// Enroll dials out and redeems a one-time enrollment code, sending this box's
// on-box public key so the control plane binds it to a new ComputeHost. On
// success it records (and returns) the assigned host id for later heartbeats.
func (c *Client) Enroll(code string) (string, error) {
	body, err := json.Marshal(enrollRequest{Code: code, PublicKey: c.ks.PublicKeyAuthorized()})
	if err != nil {
		return "", err
	}
	resp, err := c.http.Post(c.baseURL+"/api/internal/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("controlplane: enroll request: %w", err)
	}
	defer resp.Body.Close()

	var out enrollResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || !out.OK {
		msg := out.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("controlplane: enroll rejected (%d): %s", resp.StatusCode, msg)
	}
	if out.ComputeHostID == "" {
		return "", fmt.Errorf("controlplane: enroll returned no computeHostId")
	}
	c.hostID = out.ComputeHostID
	return c.hostID, nil
}

type heartbeatRequest struct {
	ComputeHostID string `json:"computeHostId"`
	// Challenge is a fresh per-beat nonce the box signs; the control plane
	// verifies Signature against the enrolled pubkey, proving key possession.
	Challenge string `json:"challenge"`
	Signature string `json:"signature"`
}

// Heartbeat sends one keypair-authenticated heartbeat: the box signs a fresh
// challenge with its on-box private key and the control plane verifies it against
// the enrolled public key. Requires a prior successful Enroll.
func (c *Client) Heartbeat() error {
	if c.hostID == "" {
		return fmt.Errorf("controlplane: cannot heartbeat before enrollment")
	}
	challenge, err := c.freshChallenge()
	if err != nil {
		return err
	}
	sig := c.ks.Sign([]byte(challenge))

	body, err := json.Marshal(heartbeatRequest{
		ComputeHostID: c.hostID,
		Challenge:     challenge,
		Signature:     base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return err
	}
	resp, err := c.http.Post(c.baseURL+"/api/internal/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("controlplane: heartbeat request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controlplane: heartbeat rejected (%d): %s", resp.StatusCode, resp.Status)
	}
	return nil
}

// freshChallenge returns the per-beat payload the box signs. It binds the host id
// and a 128-bit random nonce so a captured heartbeat can't be replayed for a
// different host. (A server-issued challenge is a stronger upgrade — see #80
// HITL notes.)
func (c *Client) freshChallenge() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("controlplane: read nonce: %w", err)
	}
	return c.hostID + ":" + base64.StdEncoding.EncodeToString(nonce), nil
}
