package pty

import (
	"fmt"
	"sync"
)

// AttachGrant is what a PtyAuthorizer returns on success: the resource the token
// is bound to (Target — a sandbox id or a host id, depending on the backing) and
// the authorized user. The transport layer binds Target against the resource id
// in the request, so a token good for resource A can't open an attach to B —
// uniformly, whichever backing answered.
type AttachGrant struct {
	Target string // sandbox id (local backing) or host id (control-plane backing)
	UserID string
}

// PtyAuthorizer decides whether a presented token may open a PTY attach. It is the
// SEAM that decouples the transport — the inbound WebSocket today, the dial-out
// reverse tunnel at #86 — from HOW an attach is authorized (#81 decision: build the
// seam now, route over the existing inbound WS, leave the reverse tunnel as #86).
// The transport layer depends on this interface, never a concrete backing.
type PtyAuthorizer interface {
	// Authorize returns the grant the token is good for, or an error if the attach
	// must be refused. Implementations MUST treat a token as single-use.
	Authorize(token string) (AttachGrant, error)
}

// ptyVerifier is the control-plane capability the host-token authorizer needs —
// satisfied by *controlplane.Client.VerifyPtyAttach. Declared here (not imported)
// so this package stays free of an import cycle and is unit-testable with a fake.
type ptyVerifier interface {
	VerifyPtyAttach(token string) (userID string, err error)
}

// controlPlaneAuthorizer authorizes an attach by redeeming the #79 host token
// against raziel-web (audience + single-use nonce verified server-side, revoked
// host rejected — slice 2). This is the authorization path for an enrolled box;
// the grant's Target is the box's own host id (what the token is audience-bound to).
type controlPlaneAuthorizer struct {
	verifier ptyVerifier
	hostID   string
}

// NewControlPlaneAuthorizer builds a seam backed by the control-plane client for
// the box identified by hostID (the id the token must be audience-bound to).
func NewControlPlaneAuthorizer(v ptyVerifier, hostID string) PtyAuthorizer {
	return &controlPlaneAuthorizer{verifier: v, hostID: hostID}
}

func (a *controlPlaneAuthorizer) Authorize(token string) (AttachGrant, error) {
	userID, err := a.verifier.VerifyPtyAttach(token)
	if err != nil {
		return AttachGrant{}, err
	}
	return AttachGrant{Target: a.hostID, UserID: userID}, nil
}

// localAuthorizer mirrors the existing inbound-WS token store: a registered token
// authorizes exactly one attach, bound to a sandbox id. It is the default/local
// backing — used when the daemon runs without a control plane (local-or-server,
// ADR-0002).
type localAuthorizer struct {
	mu     sync.Mutex
	grants map[string]AttachGrant // token -> grant; deleted on first use (single-use)
}

// NewLocalAuthorizer builds an in-memory, single-use token authorizer.
func NewLocalAuthorizer() *localAuthorizer {
	return &localAuthorizer{grants: make(map[string]AttachGrant)}
}

// Register associates a token with the sandbox + user it authorizes (one attach).
func (a *localAuthorizer) Register(token, sandboxID, userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants[token] = AttachGrant{Target: sandboxID, UserID: userID}
}

func (a *localAuthorizer) Authorize(token string) (AttachGrant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	grant, ok := a.grants[token]
	if !ok {
		return AttachGrant{}, fmt.Errorf("pty: unknown or already-used attach token")
	}
	delete(a.grants, token) // single-use
	return grant, nil
}
