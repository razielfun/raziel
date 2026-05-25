package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/auth"
	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/idgen"
	"github.com/raziel-ai/raziel/internal/manifest"
	provpkg "github.com/raziel-ai/raziel/internal/provider"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/statemachine"
)

type deploymentResponse struct {
	DeploymentID string  `json:"deployment_id"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	URL          string  `json:"url,omitempty"`
	Version      int     `json:"version"`
	IsLatest     bool    `json:"is_latest"`
	Error        string  `json:"error,omitempty"`
	Hint         string  `json:"recovery_hint,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ReadyAt      *string `json:"ready_at,omitempty"`
}

func deploymentToResponse(dep *db.Deployment) deploymentResponse {
	r := deploymentResponse{
		DeploymentID: dep.DeploymentID,
		Name:         dep.Name,
		State:        dep.State,
		URL:          dep.URL,
		Version:      dep.Version,
		IsLatest:     dep.IsLatest,
		Error:        dep.ErrorMessage,
		Hint:         dep.RecoveryHint,
		CreatedAt:    dep.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    dep.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if dep.ReadyAt != nil {
		s := dep.ReadyAt.UTC().Format(time.RFC3339)
		r.ReadyAt = &s
	}
	return r
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	deps, err := s.db.ListDeployments(db.ListDeploymentsOpts{
		TenantID: authCtx.TenantID,
		Name:     q.Get("name"),
		State:    q.Get("state"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		s.log.Error("list deployments", zap.Error(err))
		jsonInternalError(w, "failed to list deployments")
		return
	}

	items := make([]deploymentResponse, len(deps))
	for i, d := range deps {
		items[i] = deploymentToResponse(d)
	}
	jsonOK(w, map[string]any{"deployments": items, "count": len(items)})
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	deploymentID := chi.URLParam(r, "deploymentID")

	dep, err := s.db.GetDeploymentByTenant(authCtx.TenantID, deploymentID)
	if err == db.ErrNotFound {
		jsonNotFound(w)
		return
	}
	if err != nil {
		jsonInternalError(w, "failed to get deployment")
		return
	}
	jsonOK(w, deploymentToResponse(dep))
}

func (s *Server) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	if !authCtx.HasScope(auth.ScopeDeploy) {
		jsonError(w, http.StatusForbidden, "deploy scope required", "FORBIDDEN", "")
		return
	}

	// Idempotency
	if idemKey := r.Header.Get("Idempotency-Key"); idemKey != "" {
		if existing, err := s.db.LookupIdempotencyKey(idemKey); err == nil {
			dep, err := s.db.GetDeployment(existing.DeploymentID)
			if err == nil {
				jsonCreated(w, deploymentToResponse(dep))
				return
			}
		}
	}

	if err := r.ParseMultipartForm(s.cfg.MaxArtifactSizeBytes); err != nil {
		jsonBadRequest(w, fmt.Sprintf("parse form: %v", err))
		return
	}

	// Parse manifest
	manifestFile, _, err := r.FormFile("manifest")
	if err != nil {
		jsonBadRequest(w, "manifest file is required (field: manifest)")
		return
	}
	defer manifestFile.Close()
	manifestData, err := io.ReadAll(io.LimitReader(manifestFile, 64*1024))
	if err != nil {
		jsonBadRequest(w, "read manifest")
		return
	}
	m, err := manifest.Parse(manifestData)
	if err != nil {
		jsonBadRequest(w, err.Error())
		return
	}

	// Parse artifact
	artifactFile, _, err := r.FormFile("artifact")
	if err != nil {
		jsonBadRequest(w, "artifact file is required (field: artifact)")
		return
	}
	defer artifactFile.Close()
	artifactData, err := io.ReadAll(io.LimitReader(artifactFile, s.cfg.MaxArtifactSizeBytes))
	if err != nil {
		jsonBadRequest(w, "read artifact")
		return
	}

	// Parse optional secrets
	secrets := map[string]string{}
	if sv := r.FormValue("secrets"); sv != "" {
		json.Unmarshal([]byte(sv), &secrets) //nolint:errcheck
	}

	// Parse redeploy info
	previousID := r.FormValue("previous_deployment_id")
	configOnly := r.FormValue("config_only") == "true"

	// Hash source
	h := sha256.Sum256(artifactData)
	srcHash := hex.EncodeToString(h[:])

	manifestJSON, _ := json.Marshal(m)

	// Check if previous deployment exists when redeploying
	version := 1
	if previousID != "" {
		prev, err := s.db.GetDeploymentByTenant(authCtx.TenantID, previousID)
		if err == db.ErrNotFound {
			jsonBadRequest(w, fmt.Sprintf("previous deployment %q not found", previousID))
			return
		}
		if err != nil {
			jsonInternalError(w, "lookup previous deployment")
			return
		}
		version = prev.Version + 1

		// Skip build if source unchanged
		if prev.SrcHash == srcHash && !configOnly {
			configOnly = true
		}
	}

	// Store artifact (skip for config-only)
	artifactKey := ""
	if !configOnly {
		artifactKey = fmt.Sprintf("%s/%s.tar.gz", authCtx.TenantID, idgen.New("art"))
		// Ensure it's a valid tar.gz
		if !isValidTarGz(artifactData) {
			artifactData = packDir(artifactData)
		}
		if err := s.store.Put(r.Context(), artifactKey, artifactData); err != nil {
			s.log.Error("store artifact", zap.Error(err))
			jsonInternalError(w, "store artifact")
			return
		}
	}

	// Create deployment record
	depID := idgen.Deployment()
	dep := &db.Deployment{
		ID:                   idgen.New("int"),
		DeploymentID:         depID,
		TenantID:             authCtx.TenantID,
		OwnerID:              authCtx.PrincipalID,
		APIKeyID:             authCtx.APIKeyID,
		Name:                 m.Name,
		State:                string(statemachine.StateQueued),
		ArtifactKey:          artifactKey,
		ManifestJSON:         string(manifestJSON),
		SrcHash:              srcHash,
		ConfigOnly:           configOnly,
		Version:              version,
		IsLatest:             true,
		PreviousDeploymentID: previousID,
	}
	if err := s.db.CreateDeployment(dep); err != nil {
		s.log.Error("create deployment", zap.Error(err))
		jsonInternalError(w, "create deployment")
		return
	}

	// Store idempotency key
	if idemKey := r.Header.Get("Idempotency-Key"); idemKey != "" {
		s.db.StoreIdempotencyKey(&db.IdempotencyKey{ //nolint:errcheck
			ID:           idgen.New("idk"),
			IdemKey:      idemKey,
			DeploymentID: depID,
			ExpiresAt:    time.Now().Add(24 * time.Hour),
		})
	}

	// Enqueue job
	job := queue.Job{
		ID:           idgen.Job(),
		DeploymentID: depID,
		RedeployFrom: previousID,
		Secrets:      secrets,
		ConfigOnly:   configOnly,
		EnqueuedAt:   time.Now(),
	}
	if err := s.queue.Enqueue(r.Context(), job); err != nil {
		s.log.Error("enqueue job", zap.Error(err))
		// Deployment is created but won't run — mark failed
		s.db.TransitionState(depID, "queued", "failed", //nolint:errcheck
			db.WithError("failed to enqueue job", "Try redeploying"))
	}

	refreshed, _ := s.db.GetDeployment(depID)
	jsonCreated(w, deploymentToResponse(refreshed))
}

func (s *Server) handleRedeployDeployment(w http.ResponseWriter, r *http.Request) {
	// PUT is a redeploy shortcut — same as POST with previous_deployment_id set
	// For now, return 501 — full implementation in Phase 2
	jsonError(w, http.StatusNotImplemented, "use POST /v0/deployments with previous_deployment_id", "NOT_IMPLEMENTED", "")
}

func (s *Server) handleDestroyDeployment(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	deploymentID := chi.URLParam(r, "deploymentID")

	dep, err := s.db.GetDeploymentByTenant(authCtx.TenantID, deploymentID)
	if err == db.ErrNotFound {
		jsonNotFound(w)
		return
	}
	if err != nil {
		jsonInternalError(w, "get deployment")
		return
	}

	if statemachine.IsTerminal(statemachine.State(dep.State)) {
		jsonOK(w, deploymentToResponse(dep))
		return
	}

	// Destroy on provider async
	go func() {
		prov, err := s.providers.Default()
		if err != nil {
			return
		}
		res, err := s.db.GetProviderResource(deploymentID)
		if err != nil {
			return
		}
		prov.Destroy(r.Context(), provider2ProvRes(res)) //nolint:errcheck
	}()

	s.db.TransitionState(deploymentID, dep.State, "destroyed") //nolint:errcheck
	refreshed, _ := s.db.GetDeployment(deploymentID)
	jsonOK(w, deploymentToResponse(refreshed))
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	deploymentID := chi.URLParam(r, "deploymentID")

	if _, err := s.db.GetDeploymentByTenant(authCtx.TenantID, deploymentID); err == db.ErrNotFound {
		jsonNotFound(w)
		return
	}

	logType := r.URL.Query().Get("type") // build | deploy | runtime
	logs, err := s.db.GetBuildLogs(deploymentID, logType)
	if err != nil {
		jsonInternalError(w, "get logs")
		return
	}

	items := make([]map[string]string, len(logs))
	for i, l := range logs {
		items[i] = map[string]string{
			"id":        l.ID,
			"type":      l.LogType,
			"content":   l.Content,
			"timestamp": l.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	jsonOK(w, map[string]any{"logs": items})
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	deploymentID := chi.URLParam(r, "deploymentID")

	if _, err := s.db.GetDeploymentByTenant(authCtx.TenantID, deploymentID); err == db.ErrNotFound {
		jsonNotFound(w)
		return
	}

	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Hostname == "" {
		jsonBadRequest(w, "hostname is required")
		return
	}

	prov, err := s.providers.Default()
	if err != nil {
		jsonInternalError(w, "no provider")
		return
	}
	res, err := s.db.GetProviderResource(deploymentID)
	if err != nil {
		jsonBadRequest(w, "deployment has no provider resource (not yet deployed?)")
		return
	}

	info, err := prov.AddDomain(r.Context(), provider2ProvRes(res), body.Hostname)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error(), "DOMAIN_ERROR", "Check DNS configuration")
		return
	}
	jsonCreated(w, info)
}

func (s *Server) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	authCtx, _ := authFromRequest(r)
	deploymentID := chi.URLParam(r, "deploymentID")
	hostname := chi.URLParam(r, "hostname")

	if _, err := s.db.GetDeploymentByTenant(authCtx.TenantID, deploymentID); err == db.ErrNotFound {
		jsonNotFound(w)
		return
	}

	prov, err := s.providers.Default()
	if err != nil {
		jsonInternalError(w, "no provider")
		return
	}
	res, err := s.db.GetProviderResource(deploymentID)
	if err != nil {
		jsonNotFound(w)
		return
	}

	if err := prov.RemoveDomain(r.Context(), provider2ProvRes(res), hostname); err != nil {
		jsonError(w, http.StatusBadGateway, err.Error(), "DOMAIN_ERROR", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func authFromRequest(r *http.Request) (auth.Context, bool) {
	return auth.FromContext(r.Context())
}

func provider2ProvRes(r *db.ProviderResource) provpkg.ProviderResource {
	return provpkg.ProviderResource{
		AppName:    r.AppName,
		MachineID:  r.MachineID,
		Region:     r.Region,
		ImageRef:   r.ImageRef,
		ImageLabel: r.ImageLabel,
		URL:        "https://" + r.AppName + ".fly.dev",
	}
}

func isValidTarGz(data []byte) bool {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return false
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	_, err = tr.Next()
	return err == nil
}

// packDir wraps raw bytes in a minimal tar.gz as a single file named "app".
func packDir(data []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "app", Size: int64(len(data)), Mode: 0o644}) //nolint:errcheck
	tw.Write(data)                                                                  //nolint:errcheck
	tw.Close()
	gw.Close()
	return buf.Bytes()
}
