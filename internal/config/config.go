package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server
	Host string
	Port int

	// Database
	DatabaseURL  string
	DatabaseType string // "sqlite" | "postgres"

	// Auth
	AuthMode      string // "single_tenant" | "multi_tenant"
	APISecret     string
	TokenPepper   string

	// Storage
	StorageBackend string // "local" | "s3"
	StoragePath    string

	// Provider
	FlyAPIToken string
	FlyOrg      string

	// Limits
	MaxArtifactSizeBytes int64
	BuildTimeout         time.Duration
	DeployTimeout        time.Duration

	// Workers
	WorkerConcurrency int

	// Features
	BaseDomain string
	Debug      bool
}

func Load() (Config, error) {
	cfg := Config{
		Host:                 env("RAZIEL_HOST", "0.0.0.0"),
		Port:                 envInt("RAZIEL_PORT", 8000),
		DatabaseURL:          env("RAZIEL_DATABASE_URL", "raziel.db"),
		DatabaseType:         env("RAZIEL_DATABASE_TYPE", "sqlite"),
		AuthMode:             env("RAZIEL_AUTH_MODE", "single_tenant"),
		APISecret:            env("RAZIEL_API_SECRET", ""),
		TokenPepper:          env("RAZIEL_TOKEN_PEPPER", ""),
		StorageBackend:       env("RAZIEL_STORAGE_BACKEND", "local"),
		StoragePath:          env("RAZIEL_STORAGE_PATH", ".raziel/artifacts"),
		FlyAPIToken:          env("FLY_API_TOKEN", ""),
		FlyOrg:               env("FLY_ORG", ""),
		MaxArtifactSizeBytes: envInt64("RAZIEL_MAX_ARTIFACT_BYTES", 20*1024*1024),
		BuildTimeout:         envDuration("RAZIEL_BUILD_TIMEOUT", 10*time.Minute),
		DeployTimeout:        envDuration("RAZIEL_DEPLOY_TIMEOUT", 10*time.Minute),
		WorkerConcurrency:    envInt("RAZIEL_WORKER_CONCURRENCY", 4),
		BaseDomain:           env("RAZIEL_BASE_DOMAIN", ""),
		Debug:                env("RAZIEL_DEBUG", "") == "true",
	}

	if cfg.AuthMode == "single_tenant" && cfg.APISecret == "" {
		return cfg, fmt.Errorf("RAZIEL_API_SECRET is required")
	}

	return cfg, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
