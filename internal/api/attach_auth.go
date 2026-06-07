package api

import (
	"net/http"

	ptymanager "github.com/raziel-ai/raziel/internal/pty"
)

// attachError is a structured authorization failure the WS handler renders via
// jsonError. Carrying the status (not writing the response inline) keeps the
// decision pure and unit-testable.
type attachError struct {
	Status int
	Msg    string
	Code   string
	Hint   string
}

// authorizeAttach decides whether `token` may open a PTY attach to `sandboxID`,
// through the PtyAuthorizer seam. It is transport-agnostic: whichever backing
// answers (local single-use store today, control-plane host token for an enrolled
// box), the grant's Target is bound against the requested resource id — so a token
// good for resource A can never open an attach to B (P0 #2), uniformly.
func (s *Server) authorizeAttach(token, sandboxID string) (ptymanager.AttachGrant, *attachError) {
	grant, err := s.ptyAuth.Authorize(token)
	if err != nil {
		return ptymanager.AttachGrant{}, &attachError{
			Status: http.StatusUnauthorized,
			Msg:    "invalid or expired token",
			Code:   "UNAUTHORIZED",
			Hint:   "Request a new token",
		}
	}
	if grant.Target != sandboxID {
		return ptymanager.AttachGrant{}, &attachError{
			Status: http.StatusForbidden,
			Msg:    "token does not match sandbox",
			Code:   "FORBIDDEN",
		}
	}
	return grant, nil
}
