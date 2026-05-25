package sandbox_test

import (
	"context"
	"strings"
	"testing"

	"github.com/raziel-ai/raziel/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProvider(t *testing.T) sandbox.Provider {
	t.Helper()
	store, err := sandbox.NewStore(t.TempDir())
	require.NoError(t, err)
	return sandbox.NewProvider(store)
}

func TestCreateAndGet(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	sbx, err := p.Create(ctx, "sbx_test01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)
	assert.Equal(t, "sbx_test01", sbx.ID)
	assert.Equal(t, sandbox.StateRunning, sbx.State)
	assert.NotEmpty(t, sbx.WorkspacePath)

	// Can retrieve it
	got, err := p.Get("sbx_test01")
	require.NoError(t, err)
	assert.Equal(t, sbx.ID, got.ID)
	assert.Equal(t, sbx.WorkspacePath, got.WorkspacePath)
}

func TestList(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	p.Create(ctx, "sbx_a", sandbox.Config{Guardrails: sandbox.DefaultGuardrails()}) //nolint:errcheck
	p.Create(ctx, "sbx_b", sandbox.Config{Guardrails: sandbox.DefaultGuardrails()}) //nolint:errcheck

	sandboxes, err := p.List()
	require.NoError(t, err)
	assert.Len(t, sandboxes, 2)
}

func TestRunEcho(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	sbx, err := p.Create(ctx, "sbx_run01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)

	var lines []string
	code, err := p.Run(ctx, sbx, []string{"/bin/echo", "hello from sandbox"}, nil,
		func(line string) { lines = append(lines, line) },
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, len(lines))
	assert.Equal(t, "hello from sandbox", lines[0])
}

func TestRunExitCode(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	sbx, err := p.Create(ctx, "sbx_exit01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)

	code, err := p.Run(ctx, sbx, []string{"/bin/sh", "-c", "exit 42"}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 42, code)
}

func TestRunEnvVar(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	sbx, err := p.Create(ctx, "sbx_env01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)

	var out []string
	code, err := p.Run(ctx, sbx,
		[]string{"/bin/sh", "-c", "echo $MY_VAR"},
		map[string]string{"MY_VAR": "injected"},
		func(l string) { out = append(out, l) },
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, strings.Contains(strings.Join(out, ""), "injected"))
}

func TestRunRazielEnvSet(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	sbx, err := p.Create(ctx, "sbx_renv01", sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	require.NoError(t, err)

	var out []string
	p.Run(ctx, sbx, //nolint:errcheck
		[]string{"/bin/sh", "-c", "echo $RAZIEL_SANDBOX"},
		nil,
		func(l string) { out = append(out, l) },
		nil,
	)
	assert.True(t, strings.Contains(strings.Join(out, ""), "sbx_renv01"))
}

func TestStop(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	p.Create(ctx, "sbx_stop01", sandbox.Config{Guardrails: sandbox.DefaultGuardrails()}) //nolint:errcheck
	err := p.Stop(ctx, "sbx_stop01")
	require.NoError(t, err)

	sbx, err := p.Get("sbx_stop01")
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateStopped, sbx.State)
}

func TestDestroy(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	p.Create(ctx, "sbx_del01", sandbox.Config{Guardrails: sandbox.DefaultGuardrails()}) //nolint:errcheck
	err := p.Destroy(ctx, "sbx_del01")
	require.NoError(t, err)

	_, err = p.Get("sbx_del01")
	assert.Error(t, err)
}

func TestDestroyNonExistent(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	// Should not error on missing sandbox
	err := p.Destroy(ctx, "sbx_ghost")
	assert.NoError(t, err)
}
