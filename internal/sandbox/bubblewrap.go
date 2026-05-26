//go:build linux

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/creack/pty"
)

// BubblewrapProvider implements the sandbox Provider for Linux using bwrap.
type BubblewrapProvider struct {
	store *Store
}

func NewProvider(store *Store) Provider {
	return &BubblewrapProvider{store: store}
}

func (p *BubblewrapProvider) Create(ctx context.Context, id string, cfg Config) (*Sandbox, error) {
	workDir := p.store.WorkspaceDir(id)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("sandbox: create workspace %q: %w", workDir, err)
	}

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

func (p *BubblewrapProvider) Run(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdout, stderr func(string)) (int, error) {
	if len(cmd) == 0 {
		return 0, fmt.Errorf("sandbox: empty command")
	}

	args := bwrapArgs(sbx, cmd)
	c := exec.CommandContext(ctx, "bwrap", args...)
	c.Dir = sbx.WorkspacePath
	c.Env = []string{
		"HOME=" + sbx.WorkspacePath,
		"TMPDIR=/tmp",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"RAZIEL_SANDBOX=" + sbx.ID,
		"RAZIEL_WORKSPACE=" + sbx.WorkspacePath,
	}
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}

	stdoutPipe, _ := c.StdoutPipe()
	stderrPipe, _ := c.StderrPipe()

	if err := c.Start(); err != nil {
		return 1, fmt.Errorf("bwrap: %w", err)
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
	if err := c.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func (p *BubblewrapProvider) RunTTY(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdin io.Reader, stdout io.Writer, resize <-chan [2]uint16) (int, error) {
	if len(cmd) == 0 {
		return 0, fmt.Errorf("sandbox: empty command")
	}

	args := bwrapArgs(sbx, cmd)
	c := exec.CommandContext(ctx, "bwrap", args...)
	c.Dir = sbx.WorkspacePath
	c.Env = []string{
		"HOME=" + sbx.WorkspacePath,
		"TMPDIR=/tmp",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"RAZIEL_SANDBOX=" + sbx.ID,
		"RAZIEL_WORKSPACE=" + sbx.WorkspacePath,
		"TERM=xterm-256color",
	}
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}

	ptmx, err := pty.Start(c)
	if err != nil {
		return 1, fmt.Errorf("sandbox: pty start: %w", err)
	}
	defer ptmx.Close()

	// Handle resize events from the caller
	go func() {
		for sz := range resize {
			pty.Setsize(ptmx, &pty.Winsize{Cols: sz[0], Rows: sz[1]}) //nolint:errcheck
		}
	}()

	go io.Copy(ptmx, stdin)   //nolint:errcheck
	io.Copy(stdout, ptmx)     //nolint:errcheck

	if err := c.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), nil
		}
		return 0, nil
	}
	return 0, nil
}

func (p *BubblewrapProvider) Stop(_ context.Context, id string) error {
	sbx, err := p.store.Load(id)
	if err != nil {
		return err
	}
	sbx.State = StateStopped
	return p.store.Save(sbx)
}

func (p *BubblewrapProvider) Destroy(_ context.Context, id string) error {
	sbx, err := p.store.Load(id)
	if err != nil {
		return nil
	}
	sandboxDir := filepath.Dir(sbx.WorkspacePath)
	if err := os.RemoveAll(sandboxDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return p.store.Delete(id)
}

func (p *BubblewrapProvider) Get(id string) (*Sandbox, error) {
	return p.store.Load(id)
}

func (p *BubblewrapProvider) List() ([]*Sandbox, error) {
	return p.store.List()
}

func bwrapArgs(sbx *Sandbox, cmd []string) []string {
	args := []string{
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		"--bind", sbx.WorkspacePath, sbx.WorkspacePath,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--die-with-parent",
		"--chdir", sbx.WorkspacePath,
	}

	if !sbx.Config.Guardrails.Network.Enabled {
		args = append(args, "--unshare-net")
	}

	return append(args, cmd...)
}
