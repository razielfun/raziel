package db

import "time"

type Deployment struct {
	ID                   string
	DeploymentID         string
	TenantID             string
	OwnerID              string
	APIKeyID             string
	Name                 string
	State                string
	ArtifactKey          string
	ManifestJSON         string
	ErrorMessage         string
	RecoveryHint         string
	URL                  string
	Version              int
	IsLatest             bool
	PreviousDeploymentID string
	SrcHash              string
	ConfigOnly           bool
	ExpiresAt            *time.Time
	ReadyAt              *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ProviderResource struct {
	ID           string
	DeploymentID string
	Provider     string
	AppName      string
	MachineID    string
	Region       string
	ImageRef     string
	ImageLabel   string
	CreatedAt    time.Time
}

type BuildLog struct {
	ID           string
	DeploymentID string
	LogType      string // build | deploy | runtime
	Content      string
	CreatedAt    time.Time
}

type APIKey struct {
	ID          string
	TenantID    string
	PrincipalID string
	KeyPrefix   string
	KeyHash     string
	Name        string
	Scopes      string
	IsRevoked   bool
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	CreatedBy   string
	CreatedAt   time.Time
}

type IdempotencyKey struct {
	ID           string
	IdemKey      string
	DeploymentID string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
