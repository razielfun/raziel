package pty

import (
	"fmt"
	"sync"
)

// PtyAuthorizer decides whether a presented token may open a PTY attach, and to
// whom (the authorized userId). It is the SEAM that decouples the transport — the
// inbound WebSocket today, the dial-out reverse tunnel at #86 — from HOW an attach
// is authorized (#81, decision: build the seam now, route over the existing inbound
// WS, leave the reverse tunnel as #86). The transport layer depends on this
// interface, never a concrete backing.
type PtyAuthorizer interface {
	// Authorize returns the userId the token is authorized for, or an error if the
	// attach must be refused. Implementations MUST treat a token as single-use.
	Authorize(token string) (userID string, err error)
}

// ptyVerifier is the control-plane capability the host-token authorizer needs —
// satisfied by *controlplane.Client.VerifyPtyAttach. Declared here (not imported)
// so this package stays free of an import cycle and is unit-testable with a fake.
type ptyVerifier interface {
	VerifyPtyAttach(token string) (userID string, err error)
}

// controlPlaneAuthorizer authorizes an attach by redeeming the #79 host token
// against raziel-web (audience + single-use nonce verified server-side, revoked
// host rejected — slice 2). This is the authorization path for an enrolled box.
type controlPlaneAuthorizer struct {
	verifier ptyVerifier
}

// NewControlPlaneAuthorizer builds a seam backed by the control-plane client.
func NewControlPlaneAuthorizer(v ptyVerifier) PtyAuthorizer {
	return &controlPlaneAuthorizer{verifier: v}
}

func (a *controlPlaneAuthorizer) Authorize(token string) (string, error) {
	return a.verifier.VerifyPtyAttach(token)
}

// localAuthorizer mirrors the existing inbound-WS token store: a registered token
// authorizes exactly one attach. It is the default/local backing — used when the
// daemon runs without a control plane (local-or-server, ADR-0002).
type localAuthorizer struct {
	mu     sync.Mutex
	tokens map[string]string // token -> userId; deleted on first use (single-use)
}

// NewLocalAuthorizer builds an in-memory, single-use token authorizer.
func NewLocalAuthorizer() *localAuthorizer {
	return &localAuthorizer{tokens: make(map[string]string)}
}

// Register associates a token with the userId it authorizes (one attach).
func (a *localAuthorizer) Register(token, userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[token] = userID
}

func (a *localAuthorizer) Authorize(token string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	userID, ok := a.tokens[token]
	if !ok {
		return "", fmt.Errorf("pty: unknown or already-used attach token")
	}
	delete(a.tokens, token) // single-use
	return userID, nil
}
