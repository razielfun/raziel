package mock

import (
	"context"
	"time"

	"github.com/raziel-ai/raziel/internal/provider"
)

// Provider is a configurable mock for tests.
type Provider struct {
	DeployFn      func(ctx context.Context, cfg provider.DeployConfig) (provider.DeployResult, error)
	RedeployFn    func(ctx context.Context, res provider.ProviderResource, cfg provider.DeployConfig) (provider.DeployResult, error)
	GetStatusFn   func(ctx context.Context, res provider.ProviderResource) (provider.ProviderStatus, error)
	DestroyFn     func(ctx context.Context, res provider.ProviderResource) error
	GetLogsFn     func(ctx context.Context, res provider.ProviderResource, lines int) (string, error)
	HealthCheckFn func(ctx context.Context, res provider.ProviderResource, path string, timeout time.Duration) bool
}

func (m *Provider) Name() string { return "mock" }

func (m *Provider) Deploy(ctx context.Context, cfg provider.DeployConfig) (provider.DeployResult, error) {
	if m.DeployFn != nil {
		return m.DeployFn(ctx, cfg)
	}
	return provider.DeployResult{
		Resource: provider.ProviderResource{
			AppName:    "mock-app",
			MachineID:  "mock-machine",
			Region:     "iad",
			ImageRef:   "registry.fly.io/mock:latest",
			ImageLabel: "mock-label",
			URL:        "https://mock-app.fly.dev",
		},
	}, nil
}

func (m *Provider) Redeploy(ctx context.Context, res provider.ProviderResource, cfg provider.DeployConfig) (provider.DeployResult, error) {
	if m.RedeployFn != nil {
		return m.RedeployFn(ctx, res, cfg)
	}
	return provider.DeployResult{Resource: res}, nil
}

func (m *Provider) GetStatus(ctx context.Context, res provider.ProviderResource) (provider.ProviderStatus, error) {
	if m.GetStatusFn != nil {
		return m.GetStatusFn(ctx, res)
	}
	return provider.ProviderStatus{State: "running", Healthy: true, URL: res.URL}, nil
}

func (m *Provider) Destroy(ctx context.Context, res provider.ProviderResource) error {
	if m.DestroyFn != nil {
		return m.DestroyFn(ctx, res)
	}
	return nil
}

func (m *Provider) GetLogs(ctx context.Context, res provider.ProviderResource, lines int) (string, error) {
	if m.GetLogsFn != nil {
		return m.GetLogsFn(ctx, res, lines)
	}
	return "mock log line\n", nil
}

func (m *Provider) HealthCheck(ctx context.Context, res provider.ProviderResource, path string, timeout time.Duration) bool {
	if m.HealthCheckFn != nil {
		return m.HealthCheckFn(ctx, res, path, timeout)
	}
	return true
}

func (m *Provider) AddDomain(_ context.Context, _ provider.ProviderResource, hostname string) (provider.DomainInfo, error) {
	return provider.DomainInfo{Hostname: hostname, Configured: true}, nil
}

func (m *Provider) RemoveDomain(_ context.Context, _ provider.ProviderResource, _ string) error {
	return nil
}
