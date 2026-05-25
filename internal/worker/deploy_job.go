package worker

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"bytes"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/idgen"
	"github.com/raziel-ai/raziel/internal/provider"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/storage"
)

type DeployJob struct {
	job       queue.Job
	db        *db.DB
	store     storage.ArtifactStore
	providers *provider.Registry
	log       *zap.Logger
}

func (j *DeployJob) Run(ctx context.Context) {
	dep, err := j.db.GetDeployment(j.job.DeploymentID)
	if err != nil {
		j.log.Error("get deployment failed", zap.Error(err))
		return
	}

	prov, err := j.providers.Default()
	if err != nil {
		j.fail(ctx, dep, "no provider configured", "Add a cloud provider (e.g. FLY_API_TOKEN)")
		return
	}

	appName := "raziel-" + dep.DeploymentID[4:16] // strip "dep_" prefix, take 12 chars

	if dep.ConfigOnly {
		j.runConfigOnly(ctx, dep, prov, appName)
		return
	}

	j.runFull(ctx, dep, prov, appName)
}

func (j *DeployJob) runFull(ctx context.Context, dep *db.Deployment, prov provider.Provider, appName string) {
	// QUEUED → BUILDING
	if err := j.db.TransitionState(dep.DeploymentID, "queued", "building"); err != nil {
		j.log.Warn("transition to building failed (race?)", zap.Error(err))
		return
	}
	j.log.Info("building", zap.String("app", appName))

	// Extract artifact to temp dir
	workDir, err := j.extractArtifact(ctx, dep.ArtifactKey)
	if err != nil {
		j.fail(ctx, dep, fmt.Sprintf("extract artifact: %v", err), "Check that the artifact was uploaded correctly")
		return
	}
	defer os.RemoveAll(workDir)

	// Load manifest for tier spec
	var manifestData map[string]any
	json.Unmarshal([]byte(dep.ManifestJSON), &manifestData) //nolint:errcheck

	// Stage secrets if any
	if len(j.job.Secrets) > 0 {
		j.appendLog(dep.DeploymentID, "build", "Setting secrets...")
		if err := j.setFlySecrets(ctx, appName, j.job.Secrets); err != nil {
			j.appendLog(dep.DeploymentID, "build", fmt.Sprintf("Warning: secrets: %v", err))
		}
	}

	// Build and deploy via flyctl
	j.appendLog(dep.DeploymentID, "build", "Starting remote build and deploy...")
	cfg := provider.DeployConfig{
		DeploymentID: dep.DeploymentID,
		AppName:      appName,
		HealthPath:   stringFromMap(manifestData, "health_path", "/health"),
		InternalPort: intFromMap(manifestData, "port", 8080),
		AutoStop:     true,
		AutoStopTimeout: 5 * time.Minute,
	}

	result, err := j.deployFromDir(ctx, workDir, cfg, prov)
	if err != nil {
		j.appendLog(dep.DeploymentID, "build", fmt.Sprintf("Build failed: %v", err))
		j.fail(ctx, dep, err.Error(), "Check the build logs for details")
		return
	}

	j.appendLog(dep.DeploymentID, "build", result.Logs)

	// Save provider resource
	res := &db.ProviderResource{
		ID:           idgen.Resource(),
		DeploymentID: dep.DeploymentID,
		Provider:     prov.Name(),
		AppName:      result.Resource.AppName,
		MachineID:    result.Resource.MachineID,
		Region:       result.Resource.Region,
		ImageRef:     result.Resource.ImageRef,
		ImageLabel:   result.Resource.ImageLabel,
	}
	j.db.UpsertProviderResource(res) //nolint:errcheck

	// BUILDING → DEPLOYING
	if err := j.db.TransitionState(dep.DeploymentID, "building", "deploying"); err != nil {
		j.log.Warn("transition to deploying failed", zap.Error(err))
		return
	}

	// Health check
	j.appendLog(dep.DeploymentID, "deploy", fmt.Sprintf("Waiting for %s to become healthy...", result.Resource.URL))
	healthy := prov.HealthCheck(ctx, result.Resource, cfg.HealthPath, 5*time.Minute)
	if !healthy {
		j.fail(ctx, dep, "health check timed out", fmt.Sprintf("Check logs: raziel logs %s", dep.DeploymentID))
		return
	}

	// DEPLOYING → READY
	now := time.Now()
	if err := j.db.TransitionState(dep.DeploymentID, "deploying", "ready",
		db.WithURL(result.Resource.URL),
		db.WithReadyAt(now),
	); err != nil {
		j.log.Warn("transition to ready failed", zap.Error(err))
		return
	}

	// Mark previous version as not latest
	if dep.PreviousDeploymentID != "" {
		j.db.SetNotLatest(dep.PreviousDeploymentID) //nolint:errcheck
	}

	j.log.Info("deployment ready", zap.String("url", result.Resource.URL))
}

func (j *DeployJob) runConfigOnly(ctx context.Context, dep *db.Deployment, prov provider.Provider, appName string) {
	prevRes, err := j.db.GetProviderResource(dep.PreviousDeploymentID)
	if err != nil {
		j.fail(ctx, dep, "config-only deploy requires a previous successful deployment", "Deploy from source first")
		return
	}

	if err := j.db.TransitionState(dep.DeploymentID, "queued", "deploying"); err != nil {
		return
	}

	cfg := provider.DeployConfig{
		DeploymentID: dep.DeploymentID,
		AppName:      appName,
		Image:        prevRes.ImageRef,
	}
	res := provider.ProviderResource{
		AppName:    prevRes.AppName,
		MachineID:  prevRes.MachineID,
		Region:     prevRes.Region,
		ImageRef:   prevRes.ImageRef,
		ImageLabel: prevRes.ImageLabel,
		URL:        "https://" + prevRes.AppName + ".fly.dev",
	}

	result, err := prov.Redeploy(ctx, res, cfg)
	if err != nil {
		j.fail(ctx, dep, err.Error(), "Check provider logs")
		return
	}

	now := time.Now()
	j.db.TransitionState(dep.DeploymentID, "deploying", "ready", //nolint:errcheck
		db.WithURL(result.Resource.URL),
		db.WithReadyAt(now),
	)
	j.db.SetNotLatest(dep.PreviousDeploymentID) //nolint:errcheck
}

func (j *DeployJob) deployFromDir(ctx context.Context, dir string, cfg provider.DeployConfig, prov provider.Provider) (provider.DeployResult, error) {
	// Write a fly.toml if one doesn't exist
	flyToml := filepath.Join(dir, "fly.toml")
	if _, err := os.Stat(flyToml); os.IsNotExist(err) {
		toml := j.generateFlyToml(cfg)
		if err := os.WriteFile(flyToml, []byte(toml), 0o644); err != nil {
			return provider.DeployResult{}, err
		}
	}

	// Use flyctl deploy from the working dir
	cmd := exec.CommandContext(ctx, "flyctl", "deploy",
		"--app", cfg.AppName,
		"--remote-only",
		"--wait-timeout", "600",
	)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	logs := strings.TrimSpace(string(out))
	if err != nil {
		return provider.DeployResult{}, fmt.Errorf("%s\n%w", logs, err)
	}

	url := "https://" + cfg.AppName + ".fly.dev"
	return provider.DeployResult{
		Resource: provider.ProviderResource{
			AppName:    cfg.AppName,
			Region:     "iad",
			ImageLabel: cfg.DeploymentID,
			URL:        url,
		},
		Logs: logs,
	}, nil
}

func (j *DeployJob) generateFlyToml(cfg provider.DeployConfig) string {
	return fmt.Sprintf(`app = "%s"
primary_region = "iad"

[http_service]
  internal_port = %d
  force_https = true
  auto_stop_machines = true
  auto_start_machines = true
  min_machines_running = 0

  [[http_service.checks]]
    grace_period = "10s"
    interval = "30s"
    method = "GET"
    path = "%s"
    timeout = "5s"
`, cfg.AppName, cfg.InternalPort, cfg.HealthPath)
}

func (j *DeployJob) extractArtifact(ctx context.Context, key string) (string, error) {
	data, err := j.store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "raziel-build-*")
	if err != nil {
		return "", err
	}
	if err := untar(data, dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (j *DeployJob) setFlySecrets(ctx context.Context, appName string, secrets map[string]string) error {
	args := []string{"secrets", "set", "--app", appName}
	for k, v := range secrets {
		args = append(args, k+"="+v)
	}
	cmd := exec.CommandContext(ctx, "flyctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (j *DeployJob) fail(ctx context.Context, dep *db.Deployment, msg, hint string) {
	j.log.Error("deployment failed", zap.String("error", msg))
	j.db.TransitionState(dep.DeploymentID, dep.State, "failed", //nolint:errcheck
		db.WithError(msg, hint),
	)
}

func (j *DeployJob) appendLog(deploymentID, logType, content string) {
	j.db.AppendBuildLog(&db.BuildLog{ //nolint:errcheck
		ID:           idgen.Log(),
		DeploymentID: deploymentID,
		LogType:      logType,
		Content:      content,
	})
}

func untar(data []byte, dst string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return untarReader(tar.NewReader(bytes.NewReader(data)), dst)
	}
	defer gr.Close()
	return untarReader(tar.NewReader(gr), dst)
}

func untarReader(tr *tar.Reader, dst string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.Clean("/"+hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0o755) //nolint:errcheck
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755) //nolint:errcheck
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func stringFromMap(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

func intFromMap(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}
