package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/api"
	"github.com/raziel-ai/raziel/internal/config"
	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/provider"
	"github.com/raziel-ai/raziel/internal/provider/mock"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/sandbox"
	"github.com/raziel-ai/raziel/internal/storage"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	tmpDB := t.TempDir() + "/test.db"
	database, err := db.Open(tmpDB)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	tmpStore := t.TempDir()
	store, err := storage.NewLocalStore(tmpStore)
	require.NoError(t, err)

	q := queue.NewMemoryQueue(16)

	registry := provider.NewRegistry()
	registry.Register(&mock.Provider{})

	cfg := config.Config{
		Host:                 "127.0.0.1",
		Port:                 0,
		APISecret:            "test-secret",
		AuthMode:             "single_tenant",
		MaxArtifactSizeBytes: 10 * 1024 * 1024,
		StoragePath:          tmpStore,
	}

	sbxStore, err := sandbox.NewStore(t.TempDir())
	require.NoError(t, err)
	sbxProvider := sandbox.NewProvider(sbxStore)

	log := zap.NewNop()
	srv := api.New(cfg, database, store, q, registry, sbxProvider, log)
	return httptest.NewServer(srv)
}

func authHeader() string {
	return "Bearer test-secret"
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestHealthNoAuth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMeUnauthorized(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/me")
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMeAuthorized(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/me", nil)
	req.Header.Set("Authorization", authHeader())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestListDeployments(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/deployments", nil)
	req.Header.Set("Authorization", authHeader())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGetDeploymentNotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/deployments/dep_notexist", nil)
	req.Header.Set("Authorization", authHeader())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCORS(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v0/deployments", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func init() {
	os.Setenv("RAZIEL_API_SECRET", "test-secret")
}
