//go:build linux

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleGetScrollback returns the raw scrollback bytes for a sandbox+tab PTY
// session. Used by the web platform to persist logs when a session ends.
// Returns 200 with empty body if no session exists (tab never connected).
func (s *Server) handleGetScrollback(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "sandboxID")
	tabID := chi.URLParam(r, "tabID")

	data := s.ptyManager.GetScrollback(sandboxID, tabID)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}
