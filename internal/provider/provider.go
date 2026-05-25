package provider

import (
	"context"
	"time"
)

type DeployConfig struct {
	DeploymentID    string
	AppName         string
	Image           string
	CPUs            int
	CPUKind         string
	MemoryMB        int
	Region          string
	Env             map[string]string
	Secrets         map[string]string
	HealthPath      string
	InternalPort    int
	AutoStop        bool
	AutoStopTimeout time.Duration
	Volumes         []VolumeMount
}

type VolumeMount struct {
	Name   string
	Path   string
	SizeGB int
}

type ProviderResource struct {
	AppName    string
	MachineID  string
	Region     string
	ImageRef   string
	ImageLabel string
	URL        string
}

type DeployResult struct {
	Resource ProviderResource
	Logs     string
}

type ProviderStatus struct {
	State   string // running | stopped | starting | failed
	Healthy bool
	URL     string
	Error   string
}

type DomainInfo struct {
	Hostname          string
	Configured        bool
	CertificateStatus string
	Error             string
}

type Provider interface {
	Name() string
	Deploy(ctx context.Context, cfg DeployConfig) (DeployResult, error)
	Redeploy(ctx context.Context, res ProviderResource, cfg DeployConfig) (DeployResult, error)
	GetStatus(ctx context.Context, res ProviderResource) (ProviderStatus, error)
	Destroy(ctx context.Context, res ProviderResource) error
	GetLogs(ctx context.Context, res ProviderResource, lines int) (string, error)
	HealthCheck(ctx context.Context, res ProviderResource, path string, timeout time.Duration) bool
	AddDomain(ctx context.Context, res ProviderResource, hostname string) (DomainInfo, error)
	RemoveDomain(ctx context.Context, res ProviderResource, hostname string) error
}
