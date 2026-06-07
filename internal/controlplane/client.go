package controlplane

import (
	"bytes"
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

type challengeRequest struct {
	ComputeHostID string `json:"computeHostId"`
}

type challengeResponse struct {
	OK        bool   `json:"ok"`
	Challenge string `json:"challenge"`
	Error     string `json:"error"`
}

type heartbeatRequest struct {
	ComputeHostID string `json:"computeHostId"`
	// Challenge is a SERVER-ISSUED, short-TTL, single-use token the box just
	// fetched; the box signs it and the control plane re-verifies the challenge
	// AND the Signature against the enrolled pubkey. Server-issuing the challenge
	// (vs the box inventing one) makes a captured heartbeat un-replayable.
	Challenge string `json:"challenge"`
	Signature string `json:"signature"`
}

// Heartbeat sends one keypair-authenticated heartbeat using challenge-response:
// the box first requests a server-issued challenge, signs THAT with its on-box
// private key, and posts it back. The control plane re-verifies the challenge
// (live, host-bound, single-use) and the signature against the enrolled public
// key. Requires a prior successful Enroll.
func (c *Client) Heartbeat() error {
	if c.hostID == "" {
		return fmt.Errorf("controlplane: cannot heartbeat before enrollment")
	}
	challenge, err := c.requestChallenge()
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

// requestChallenge fetches a fresh server-issued challenge for this box. The box
// never invents its own challenge — that is what bounds replay to the challenge's
// short TTL + single use.
func (c *Client) requestChallenge() (string, error) {
	body, err := json.Marshal(challengeRequest{ComputeHostID: c.hostID})
	if err != nil {
		return "", err
	}
	resp, err := c.http.Post(c.baseURL+"/api/internal/heartbeat/challenge", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("controlplane: challenge request: %w", err)
	}
	defer resp.Body.Close()

	var out challengeResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || !out.OK || out.Challenge == "" {
		msg := out.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("controlplane: challenge rejected (%d): %s", resp.StatusCode, msg)
	}
	return out.Challenge, nil
}
