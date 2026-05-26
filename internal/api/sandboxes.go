package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/sandbox"
)

type sandboxResponse struct {
	ID            string          `json:"id"`
	State         string          `json:"state"`
	Agent         string          `json:"agent,omitempty"`
	WorkspacePath string          `json:"workspace_path"`
	CreatedAt     string          `json:"created_at"`
}

func sbxToResponse(sbx *sandbox.Sandbox) sandboxResponse {
	return sandboxResponse{
		ID:            sbx.ID,
		State:         string(sbx.State),
		Agent:         sbx.Config.Agent,
		WorkspacePath: sbx.WorkspacePath,
		CreatedAt:     sbx.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := s.sandboxProvider.List()
	if err != nil {
		s.log.Error("list sandboxes", zap.Error(err))
		jsonInternalError(w, "failed to list sandboxes")
		return
	}
	items := make([]sandboxResponse, len(sandboxes))
	for i, sbx := range sandboxes {
		items[i] = sbxToResponse(sbx)
	}
	jsonOK(w, map[string]any{"sandboxes": items, "count": len(items)})
}

func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		Agent       string `json:"agent"`
		CloneURL    string `json:"clone_url"`
		Branch      string `json:"branch"`
		Worktree    bool   `json:"worktree"`
		GitHubToken string `json:"github_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonBadRequest(w, "invalid JSON body")
		return
	}
	if body.ID == "" {
		body.ID = uuid.New().String()[:8]
	}

	cfg := sandbox.Config{
		Agent:      body.Agent,
		Guardrails: sandbox.DefaultGuardrails(),
	}
	sbx, err := s.sandboxProvider.Create(r.Context(), body.ID, cfg)
	if err != nil {
		s.log.Error("create sandbox", zap.Error(err))
		jsonInternalError(w, fmt.Sprintf("create sandbox: %v", err))
		return
	}

	// Clone repository into workspace if requested
	if body.CloneURL != "" {
		go s.cloneRepo(sbx, body.CloneURL, body.Branch, body.GitHubToken, body.Worktree)
	}

	jsonCreated(w, sbxToResponse(sbx))
}

func (s *Server) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	sbx, err := s.sandboxProvider.Get(id)
	if err != nil {
		jsonNotFound(w)
		return
	}
	jsonOK(w, sbxToResponse(sbx))
}

func (s *Server) handleStopSandbox(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	if _, err := s.sandboxProvider.Get(id); err != nil {
		jsonNotFound(w)
		return
	}
	if err := s.sandboxProvider.Stop(r.Context(), id); err != nil {
		s.log.Error("stop sandbox", zap.String("id", id), zap.Error(err))
		jsonInternalError(w, "failed to stop sandbox")
		return
	}
	sbx, _ := s.sandboxProvider.Get(id)
	jsonOK(w, sbxToResponse(sbx))
}

func (s *Server) handleDestroySandbox(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	if _, err := s.sandboxProvider.Get(id); err != nil {
		jsonNotFound(w)
		return
	}
	if err := s.sandboxProvider.Destroy(r.Context(), id); err != nil {
		s.log.Error("destroy sandbox", zap.String("id", id), zap.Error(err))
		jsonInternalError(w, "failed to destroy sandbox")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRegisterWsToken stores a short-lived token so the browser can open a
// WebSocket directly to the daemon. The web platform calls this endpoint, then
// hands the token to the browser which connects to /v0/sandboxes/{id}/ws.
func (s *Server) handleRegisterWsToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	if _, err := s.sandboxProvider.Get(id); err != nil {
		jsonNotFound(w)
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		jsonBadRequest(w, "token is required")
		return
	}

	s.wsTokens.Register(body.Token, id)
	jsonOK(w, map[string]any{
		"ok":         true,
		"sandbox_id": id,
		"expires_in": 60,
	})
}

// cloneRepo runs git clone in the sandbox workspace. Runs in a goroutine after
// the sandbox creation response is already sent. Injects a GitHub token into the
// clone URL if one is provided.
func (s *Server) cloneRepo(sbx *sandbox.Sandbox, cloneURL, branch, githubToken string, worktree bool) {
	target := cloneURL
	if githubToken != "" {
		if u, err := url.Parse(cloneURL); err == nil {
			u.User = url.UserPassword("x-access-token", githubToken)
			target = u.String()
		}
	}

	args := []string{"clone", "--depth=1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, target, sbx.WorkspacePath)

	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.log.Warn("sandbox git clone failed",
			zap.String("id", sbx.ID),
			zap.String("url", cloneURL),
			zap.Error(err),
			zap.String("output", string(out)),
		)
	}
}
