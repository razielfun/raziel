package api

import (
	"net/http"
	"runtime"
	"time"
)

var startTime = time.Now()

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{
		"ok":      true,
		"version": "0.1.0",
		"uptime":  time.Since(startTime).String(),
		"go":      runtime.Version(),
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	jsonOK(w, map[string]any{
		"tenant_id":    authCtx.TenantID,
		"principal_id": authCtx.PrincipalID,
		"scopes":       authCtx.Scopes,
	})
}
