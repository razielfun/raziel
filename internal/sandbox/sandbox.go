package sandbox

import (
	"context"
	"io"
	"time"
)

type State string

const (
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateDestroyed State = "destroyed"
)

type NetworkConfig struct {
	Enabled      bool
	AllowDomains []string
}

type Guardrails struct {
	Network         NetworkConfig
	AllowWritePaths []string
	DenyWritePaths  []string
	TimeoutMinutes  int
}

type Config struct {
	Agent        string            // "claude-code" | "codex" | "gemini" | ""
	Template     string            // optional template to scaffold
	Guardrails   Guardrails
	PortMappings map[int]int
	EnvVars      map[string]string // injected into PTY process env (API keys etc.)
	Prompt       string            // written to tab-0 stdin after agent starts
	Username     string            // non-root user the sandbox runs as (Clerk username)
}

type Sandbox struct {
	ID            string
	State         State
	WorkspacePath string
	Config        Config
	CreatedAt     time.Time
	AgentStarted  bool // true once tab-0's agent has been launched at least once (drives resume)
}

// DefaultGuardrails returns permissive defaults suitable for development.
func DefaultGuardrails() Guardrails {
	return Guardrails{
		Network:        NetworkConfig{Enabled: true},
		AllowWritePaths: []string{"."},
		TimeoutMinutes: 60,
	}
}

// Provider is the interface for OS-level sandbox implementations.
type Provider interface {
	Create(ctx context.Context, id string, cfg Config) (*Sandbox, error)
	// Run executes a command inside the sandbox and streams output via the
	// provided callbacks. Returns when the command exits.
	Run(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdout, stderr func(string)) (int, error)
	// RunTTY executes a command with a full PTY attached, wiring stdio to the
	// provided reader/writer (e.g. a WebSocket pipe). Resize events are sent on
	// the resize channel as [cols, rows] pairs.
	RunTTY(ctx context.Context, sbx *Sandbox, cmd []string, env map[string]string, stdin io.Reader, stdout io.Writer, resize <-chan [2]uint16) (int, error)
	Stop(ctx context.Context, id string) error
	Destroy(ctx context.Context, id string) error
	Get(id string) (*Sandbox, error)
	List() ([]*Sandbox, error)
	// MarkAgentStarted flips the AgentStarted flag and persists it, so a later
	// launch knows to resume the agent's conversation instead of starting fresh.
	MarkAgentStarted(id string) error
}
