package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/raziel-ai/raziel/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersistAcrossProviderRestart simulates process restart by creating a
// sandbox with one Provider instance, then loading it with a brand-new
// Provider instance that shares only the on-disk store directory.
func TestPersistAcrossProviderRestart(t *testing.T) {
	storeDir := t.TempDir()
	ctx := context.Background()

	// --- Process 1: create sandbox and write a file ---
	store1, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p1 := sandbox.NewProvider(store1)

	sbx, err := p1.Create(ctx, "sbx_persist01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)

	// Write a sentinel file inside the workspace
	sentinel := filepath.Join(sbx.WorkspacePath, "hello.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("still here"), 0o644))

	// --- Process 2: new Provider, same store directory ---
	store2, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p2 := sandbox.NewProvider(store2)

	// State is readable
	got, err := p2.Get("sbx_persist01")
	require.NoError(t, err)
	assert.Equal(t, "sbx_persist01", got.ID)
	assert.Equal(t, sandbox.StateRunning, got.State)
	assert.Equal(t, sbx.WorkspacePath, got.WorkspacePath)

	// Workspace file is still on disk
	data, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	assert.Equal(t, "still here", string(data))

	// Can run commands in the recovered sandbox
	var out []string
	code, err := p2.Run(ctx, got, []string{"/bin/cat", sentinel}, nil,
		func(l string) { out = append(out, l) }, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"still here"}, out)
}

// TestPersistStateTransitions checks that Stop/Running state survives reload.
func TestPersistStateTransitions(t *testing.T) {
	storeDir := t.TempDir()
	ctx := context.Background()

	store1, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p1 := sandbox.NewProvider(store1)

	_, err = p1.Create(ctx, "sbx_state01", sandbox.Config{Guardrails: sandbox.DefaultGuardrails()})
	require.NoError(t, err)

	require.NoError(t, p1.Stop(ctx, "sbx_state01"))

	// New provider reads the persisted stopped state
	store2, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p2 := sandbox.NewProvider(store2)

	got, err := p2.Get("sbx_state01")
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateStopped, got.State)
}

// TestPersistListAll verifies List() returns all sandboxes from disk.
func TestPersistListAll(t *testing.T) {
	storeDir := t.TempDir()
	ctx := context.Background()

	store1, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p1 := sandbox.NewProvider(store1)

	for _, id := range []string{"sbx_la", "sbx_lb", "sbx_lc"} {
		_, err := p1.Create(ctx, id, sandbox.Config{Guardrails: sandbox.DefaultGuardrails()})
		require.NoError(t, err)
	}

	// Fresh provider, same dir
	store2, err := sandbox.NewStore(storeDir)
	require.NoError(t, err)
	p2 := sandbox.NewProvider(store2)

	sandboxes, err := p2.List()
	require.NoError(t, err)
	assert.Len(t, sandboxes, 3)
}
