package manifest_test

import (
	"testing"

	"github.com/raziel-ai/raziel/internal/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseValid(t *testing.T) {
	yaml := []byte(`
name: my-api
template: backend-service
runtime: python
port: 3000
tier: performance
health_path: /ping
env_schema:
  - name: DATABASE_URL
    type: url
    required: true
    secret: true
volumes:
  - name: data
    path: /data
    size_gb: 5
`)
	m, err := manifest.Parse(yaml)
	require.NoError(t, err)
	assert.Equal(t, "my-api", m.Name)
	assert.Equal(t, "backend-service", m.Template)
	assert.Equal(t, 3000, m.Port)
	assert.Equal(t, "performance", m.Tier)
	assert.Equal(t, "/ping", m.HealthPath)
	assert.Len(t, m.EnvSchema, 1)
	assert.True(t, m.EnvSchema[0].Secret)
	assert.Len(t, m.Volumes, 1)
}

func TestParseDefaults(t *testing.T) {
	yaml := []byte(`
name: hello
template: static-site
`)
	m, err := manifest.Parse(yaml)
	require.NoError(t, err)
	assert.Equal(t, "starter", m.Tier)
	assert.Equal(t, "/health", m.HealthPath)
	assert.Equal(t, 8080, m.Port)
}

func TestParseInvalidName(t *testing.T) {
	cases := []string{
		"NAME",
		"has space",
		"with!special",
		"",
	}
	for _, name := range cases {
		yaml := []byte("name: " + name + "\ntemplate: docker\n")
		_, err := manifest.Parse(yaml)
		assert.Error(t, err, "expected error for name %q", name)
	}
}

func TestParseInvalidTemplate(t *testing.T) {
	yaml := []byte("name: good\ntemplate: nonexistent\n")
	_, err := manifest.Parse(yaml)
	assert.Error(t, err)
}

func TestTierSpec(t *testing.T) {
	cases := map[string]manifest.TierSpec{
		"starter":     {CPUs: 2, CPUKind: "shared", MemoryMB: 2048},
		"performance": {CPUs: 4, CPUKind: "shared", MemoryMB: 4096},
		"pro":         {CPUs: 4, CPUKind: "shared", MemoryMB: 8192},
	}
	for tier, expected := range cases {
		m := manifest.Manifest{Name: "t", Template: "docker", Tier: tier}
		assert.Equal(t, expected, m.TierSpec(), "tier=%s", tier)
	}
}
