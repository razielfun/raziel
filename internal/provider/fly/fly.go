package fly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/raziel-ai/raziel/internal/provider"
)

type FlyProvider struct {
	apiToken string
	org      string
	client   *http.Client
}

func New(apiToken, org string) *FlyProvider {
	return &FlyProvider{
		apiToken: apiToken,
		org:      org,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *FlyProvider) Name() string { return "fly" }

func (f *FlyProvider) Deploy(ctx context.Context, cfg provider.DeployConfig) (provider.DeployResult, error) {
	if err := f.ensureApp(ctx, cfg.AppName); err != nil {
		return provider.DeployResult{}, fmt.Errorf("fly: ensure app: %w", err)
	}

	logs, err := f.flyctlDeploy(ctx, cfg, false)
	if err != nil {
		return provider.DeployResult{}, fmt.Errorf("fly: deploy: %w", err)
	}

	url := fmt.Sprintf("https://%s.fly.dev", cfg.AppName)
	return provider.DeployResult{
		Resource: provider.ProviderResource{
			AppName:    cfg.AppName,
			Region:     cfg.Region,
			ImageLabel: cfg.DeploymentID,
			URL:        url,
		},
		Logs: logs,
	}, nil
}

func (f *FlyProvider) Redeploy(ctx context.Context, res provider.ProviderResource, cfg provider.DeployConfig) (provider.DeployResult, error) {
	logs, err := f.flyctlDeploy(ctx, cfg, cfg.Image != "")
	if err != nil {
		return provider.DeployResult{}, fmt.Errorf("fly: redeploy: %w", err)
	}
	res.ImageLabel = cfg.DeploymentID
	return provider.DeployResult{Resource: res, Logs: logs}, nil
}

func (f *FlyProvider) flyctlDeploy(ctx context.Context, cfg provider.DeployConfig, imageOnly bool) (string, error) {
	args := []string{"deploy",
		"--app", cfg.AppName,
		"--remote-only",
		"--wait-timeout", "600",
	}
	if imageOnly && cfg.Image != "" {
		args = append(args, "--image", cfg.Image)
	}

	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Env = append(cmd.Environ(), "FLY_API_TOKEN="+f.apiToken)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("flyctl exited: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (f *FlyProvider) GetStatus(ctx context.Context, res provider.ProviderResource) (provider.ProviderStatus, error) {
	machines, err := f.listMachines(ctx, res.AppName)
	if err != nil {
		return provider.ProviderStatus{}, err
	}
	if len(machines) == 0 {
		return provider.ProviderStatus{State: "stopped"}, nil
	}
	m := machines[0]
	return provider.ProviderStatus{
		State:   m["state"].(string),
		Healthy: m["state"] == "started",
		URL:     res.URL,
	}, nil
}

func (f *FlyProvider) Destroy(ctx context.Context, res provider.ProviderResource) error {
	cmd := exec.CommandContext(ctx, "flyctl", "apps", "destroy", res.AppName, "--yes")
	cmd.Env = append(cmd.Environ(), "FLY_API_TOKEN="+f.apiToken)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fly destroy %s: %w\n%s", res.AppName, err, out)
	}
	return nil
}

func (f *FlyProvider) GetLogs(ctx context.Context, res provider.ProviderResource, lines int) (string, error) {
	args := []string{"logs", "--app", res.AppName}
	if lines > 0 {
		args = append(args, "--num", fmt.Sprintf("%d", lines))
	}
	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Env = append(cmd.Environ(), "FLY_API_TOKEN="+f.apiToken)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *FlyProvider) HealthCheck(ctx context.Context, res provider.ProviderResource, path string, timeout time.Duration) bool {
	url := res.URL + path
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode < 500 {
			resp.Body.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Second):
		}
	}
	return false
}

func (f *FlyProvider) AddDomain(_ context.Context, _ provider.ProviderResource, hostname string) (provider.DomainInfo, error) {
	return provider.DomainInfo{Hostname: hostname, Configured: false, CertificateStatus: "pending"}, nil
}

func (f *FlyProvider) RemoveDomain(_ context.Context, _ provider.ProviderResource, _ string) error {
	return nil
}

func (f *FlyProvider) ensureApp(ctx context.Context, appName string) error {
	body, _ := json.Marshal(map[string]any{
		"app_name": appName,
		"org_slug": f.org,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.fly.io/v1/apps", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 201 = created, 422 = already exists — both OK
	if resp.StatusCode != 201 && resp.StatusCode != 422 {
		return fmt.Errorf("create app: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (f *FlyProvider) listMachines(ctx context.Context, appName string) ([]map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.machines.dev/v1/apps/"+appName+"/machines", nil)
	req.Header.Set("Authorization", "Bearer "+f.apiToken)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var machines []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&machines); err != nil {
		return nil, err
	}
	return machines, nil
}

