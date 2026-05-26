package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/raziel-ai/raziel/internal/idgen"
	"github.com/raziel-ai/raziel/internal/sandbox"
)

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage local sandboxes",
}

var sandboxCreateCmd = &cobra.Command{
	Use:   "create [id]",
	Short: "Create a local sandbox",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSandboxCreate,
}

var sandboxRunCmd = &cobra.Command{
	Use:   "run <id> -- <cmd> [args...]",
	Short: "Run a command inside a sandbox",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSandboxRun,
}

var sandboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List local sandboxes",
	RunE:  runSandboxList,
}

var sandboxStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a sandbox (keeps workspace)",
	Args:  cobra.ExactArgs(1),
	RunE:  runSandboxStop,
}

var sandboxDestroyCmd = &cobra.Command{
	Use:   "destroy <id>",
	Short: "Destroy a sandbox and its workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runSandboxDestroy,
}

func init() {
	sandboxCmd.AddCommand(sandboxCreateCmd)
	sandboxCmd.AddCommand(sandboxRunCmd)
	sandboxCmd.AddCommand(sandboxListCmd)
	sandboxCmd.AddCommand(sandboxStopCmd)
	sandboxCmd.AddCommand(sandboxDestroyCmd)
	rootCmd.AddCommand(sandboxCmd)
}

func newSandboxProvider() (sandbox.Provider, error) {
	store, err := sandbox.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("sandbox store: %w", err)
	}
	return sandbox.NewProvider(store), nil
}

func runSandboxCreate(_ *cobra.Command, args []string) error {
	id := idgen.Sandbox()
	if len(args) > 0 {
		id = args[0]
	}

	p, err := newSandboxProvider()
	if err != nil {
		return err
	}

	sbx, err := p.Create(context.Background(), id, sandbox.Config{
		Guardrails: sandbox.DefaultGuardrails(),
	})
	if err != nil {
		return err
	}

	outputJSON(map[string]any{
		"id":             sbx.ID,
		"state":          string(sbx.State),
		"workspace_path": sbx.WorkspacePath,
		"created_at":     sbx.CreatedAt,
	})
	return nil
}

func runSandboxRun(_ *cobra.Command, args []string) error {
	id := args[0]
	// Everything after "--" is the command
	cmd := args[1:]
	if len(cmd) == 0 {
		return fmt.Errorf("command required after sandbox id (use: raziel sandbox run <id> -- <cmd>)")
	}

	p, err := newSandboxProvider()
	if err != nil {
		return err
	}

	sbx, err := p.Get(id)
	if err != nil {
		return fmt.Errorf("sandbox %q: %w", id, err)
	}

	var code int
	if isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		// Interactive terminal — allocate a PTY so editors/REPLs work correctly
		code, err = p.RunTTY(context.Background(), sbx, cmd, nil)
	} else {
		// Non-interactive (piped) — stream lines via callbacks
		code, err = p.Run(context.Background(), sbx, cmd, nil,
			func(line string) { fmt.Fprintln(os.Stdout, line) },
			func(line string) { fmt.Fprintln(os.Stderr, line) },
		)
	}
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func runSandboxList(_ *cobra.Command, _ []string) error {
	p, err := newSandboxProvider()
	if err != nil {
		return err
	}

	sandboxes, err := p.List()
	if err != nil {
		return err
	}

	items := make([]map[string]any, len(sandboxes))
	for i, sbx := range sandboxes {
		items[i] = map[string]any{
			"id":             sbx.ID,
			"state":          string(sbx.State),
			"workspace_path": sbx.WorkspacePath,
			"created_at":     sbx.CreatedAt,
		}
	}
	outputJSON(map[string]any{"sandboxes": items, "count": len(items)})
	return nil
}

func runSandboxStop(_ *cobra.Command, args []string) error {
	p, err := newSandboxProvider()
	if err != nil {
		return err
	}
	if err := p.Stop(context.Background(), args[0]); err != nil {
		return err
	}
	outputJSON(map[string]string{"id": args[0], "state": "stopped"})
	return nil
}

func runSandboxDestroy(_ *cobra.Command, args []string) error {
	p, err := newSandboxProvider()
	if err != nil {
		return err
	}
	if err := p.Destroy(context.Background(), args[0]); err != nil {
		return err
	}
	outputJSON(map[string]string{"id": args[0], "state": "destroyed"})
	return nil
}
