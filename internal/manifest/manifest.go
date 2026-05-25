package manifest

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type EnvVar struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Secret      bool   `yaml:"secret"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
}

type VolumeMount struct {
	Name   string `yaml:"name"`
	Path   string `yaml:"path"`
	SizeGB int    `yaml:"size_gb"`
}

type Features struct {
	Database bool `yaml:"database"`
	Auth     bool `yaml:"auth"`
}

type Connection struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

type Manifest struct {
	Name        string        `yaml:"name"`
	Template    string        `yaml:"template"`
	Runtime     string        `yaml:"runtime"`
	HealthPath  string        `yaml:"health_path"`
	Port        int           `yaml:"port"`
	Tier        string        `yaml:"tier"`
	EnvSchema   []EnvVar      `yaml:"env_schema"`
	Connections []Connection  `yaml:"connections"`
	Features    Features      `yaml:"features"`
	Volumes     []VolumeMount `yaml:"volumes"`
}

var validTemplates = map[string]bool{
	"backend-service": true,
	"static-site":     true,
	"web-app":         true,
	"docker":          true,
}

var validTiers = map[string]bool{
	"starter":     true,
	"performance": true,
	"pro":         true,
}

var validEnvTypes = map[string]bool{
	"string":  true,
	"url":     true,
	"number":  true,
	"boolean": true,
}

func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("manifest: parse failed: %w", err)
	}
	if err := m.Validate(); err != nil {
		return m, err
	}
	return m, nil
}

func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("manifest: name must be lowercase letters, numbers, hyphens (got %q)", m.Name)
	}
	if !validTemplates[m.Template] {
		return fmt.Errorf("manifest: unknown template %q (valid: %s)", m.Template, strings.Join(keys(validTemplates), ", "))
	}
	if m.Tier == "" {
		m.Tier = "starter"
	}
	if !validTiers[m.Tier] {
		return fmt.Errorf("manifest: unknown tier %q (valid: %s)", m.Tier, strings.Join(keys(validTiers), ", "))
	}
	if m.HealthPath == "" {
		m.HealthPath = "/health"
	}
	if m.Port == 0 {
		m.Port = 8080
	}
	if m.Port < 1 || m.Port > 65535 {
		return fmt.Errorf("manifest: port must be 1-65535 (got %d)", m.Port)
	}
	for i, ev := range m.EnvSchema {
		if ev.Name == "" {
			return fmt.Errorf("manifest: env_schema[%d]: name is required", i)
		}
		if ev.Type == "" {
			m.EnvSchema[i].Type = "string"
		} else if !validEnvTypes[ev.Type] {
			return fmt.Errorf("manifest: env_schema[%d]: unknown type %q", i, ev.Type)
		}
	}
	for i, v := range m.Volumes {
		if v.Name == "" {
			return fmt.Errorf("manifest: volumes[%d]: name is required", i)
		}
		if v.Path == "" {
			return fmt.Errorf("manifest: volumes[%d]: path is required", i)
		}
		if v.SizeGB < 1 || v.SizeGB > 100 {
			m.Volumes[i].SizeGB = 1
		}
	}
	return nil
}

func (m *Manifest) TierSpec() TierSpec {
	switch m.Tier {
	case "performance":
		return TierSpec{CPUs: 4, CPUKind: "shared", MemoryMB: 4096}
	case "pro":
		return TierSpec{CPUs: 4, CPUKind: "shared", MemoryMB: 8192}
	default:
		return TierSpec{CPUs: 2, CPUKind: "shared", MemoryMB: 2048}
	}
}

type TierSpec struct {
	CPUs     int
	CPUKind  string
	MemoryMB int
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
