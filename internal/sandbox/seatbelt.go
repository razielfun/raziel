//go:build darwin

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/creack/pty"
)

// SeatbeltProvider implements the sandbox Provider for macOS using
// sandbox-exec (seatbelt). Each command runs under a generated SBPL policy
// that restricts filesystem writes and optionally network access.
type SeatbeltProvider struct {
	store *Store
}

func NewProvider(store *Store) Provider {
	return &SeatbeltProvider{store: store}
}

func (p *SeatbeltProvider) Create(ctx context.Context, id string, cfg Config) (*Sandbox, error) {
	workDir := p.store.WorkspaceDir(id)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("sandbox: create workspace %q: %w", workDir, err)
	}

	// Set sensible defaults
	if len(cfg.Guardrails.AllowWritePaths) == 0 {
		cfg.Guardrails.AllowWritePaths = []string{"."}
	}
	if cfg.Guardrails.TimeoutMinutes == 0 {
		cfg.Guardrails.TimeoutMinutes = 60
	}
	if cfg.PortMappings == nil {
		cfg.PortMappings = map[int]int{3000: 3000, 8080: 8080}
	}

	sbx := &Sandbox{
		ID:            id,
		State:         StateRunning,
		WorkspacePath: workDir,
		Config:        cfg,
		CreatedAt:     time.Now(),
	}
	if err := p.store.Save(sbx); err != nil {
		return nil, err
	}
	return sbx, nil
}

func (p *SeatbeltProvider) Run(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdout, stderr func(string)) (int, error) {
	if len(cmd) == 0 {
		return 0, fmt.Errorf("sandbox: empty command")
	}

	policy := generateSBPL(sbx)

	// Build: sandbox-exec -p <policy> <cmd...>
	args := append([]string{"-p", policy}, cmd...)
	c := exec.CommandContext(ctx, "sandbox-exec", args...)
	c.Dir = sbx.WorkspacePath

	// Merge environment
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Env = append(c.Env,
		"RAZIEL_SANDBOX="+sbx.ID,
		"RAZIEL_WORKSPACE="+sbx.WorkspacePath,
	)

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return 1, err
	}
	stderrPipe, err := c.StderrPipe()
	if err != nil {
		return 1, err
	}

	if err := c.Start(); err != nil {
		return 1, fmt.Errorf("sandbox-exec: %w", err)
	}

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			if stdout != nil {
				stdout(scanner.Text())
			}
		}
		close(done)
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			if stderr != nil {
				stderr(scanner.Text())
			}
		}
	}()

	<-done
	err = c.Wait()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func (p *SeatbeltProvider) RunTTY(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdin io.Reader, stdout io.Writer, resize <-chan [2]uint16) (int, error) {
	if len(cmd) == 0 {
		return 0, fmt.Errorf("sandbox: empty command")
	}

	policy := generateSBPL(sbx)
	args := append([]string{"-p", policy}, cmd...)
	c := exec.CommandContext(ctx, "sandbox-exec", args...)
	c.Dir = sbx.WorkspacePath

	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Env = append(c.Env,
		"RAZIEL_SANDBOX="+sbx.ID,
		"RAZIEL_WORKSPACE="+sbx.WorkspacePath,
		"TERM=xterm-256color",
	)

	ptmx, err := pty.Start(c)
	if err != nil {
		return 1, fmt.Errorf("sandbox: pty start: %w", err)
	}
	defer ptmx.Close()

	go func() {
		for sz := range resize {
			pty.Setsize(ptmx, &pty.Winsize{Cols: sz[0], Rows: sz[1]}) //nolint:errcheck
		}
	}()

	go io.Copy(ptmx, stdin)  //nolint:errcheck
	io.Copy(stdout, ptmx)    //nolint:errcheck

	if err := c.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return 0, nil
	}
	return 0, nil
}


func (p *SeatbeltProvider) Stop(_ context.Context, id string) error {
	sbx, err := p.store.Load(id)
	if err != nil {
		return err
	}
	sbx.State = StateStopped
	return p.store.Save(sbx)
}

func (p *SeatbeltProvider) Destroy(_ context.Context, id string) error {
	sbx, err := p.store.Load(id)
	if err != nil {
		// Already gone — OK
		return nil
	}
	// Remove workspace directory
	sandboxDir := filepath.Dir(sbx.WorkspacePath)
	if err := os.RemoveAll(sandboxDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return p.store.Delete(id)
}

func (p *SeatbeltProvider) Get(id string) (*Sandbox, error) {
	return p.store.Load(id)
}

func (p *SeatbeltProvider) MarkAgentStarted(id string) error {
	sbx, err := p.store.Load(id)
	if err != nil {
		return err
	}
	if sbx.AgentStarted {
		return nil
	}
	sbx.AgentStarted = true
	return p.store.Save(sbx)
}

func (p *SeatbeltProvider) List() ([]*Sandbox, error) {
	return p.store.List()
}

// generateSBPL produces a macOS Sandbox Profile Language policy for the given sandbox.
// The policy:
//   - Allows all operations by default (permissive base)
//   - Denies writes to paths outside the workspace (if deny paths configured)
//   - Optionally restricts outbound network
func generateSBPL(sbx *Sandbox) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	// Deny writes to explicitly denied paths
	for _, p := range sbx.Config.Guardrails.DenyWritePaths {
		abs := p
		if !filepath.IsAbs(p) {
			abs = filepath.Join(sbx.WorkspacePath, p)
		}
		fmt.Fprintf(&b, "(deny file-write* (subpath %q))\n", abs)
	}

	// Network restrictions
	if !sbx.Config.Guardrails.Network.Enabled {
		b.WriteString("(deny network*)\n")
		// Always allow localhost so dev servers work
		b.WriteString("(allow network* (local ip))\n")
	}

	return b.String()
}
