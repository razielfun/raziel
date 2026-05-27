package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Store persists sandbox state to ~/.raziel/sandboxes/{id}/state.json
type Store struct {
	root string
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("sandbox store: mkdir %q: %w", root, err)
	}
	return &Store{root: root}, nil
}

func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(home, ".raziel", "sandboxes"))
}

type sandboxJSON struct {
	ID            string      `json:"id"`
	State         string      `json:"state"`
	WorkspacePath string      `json:"workspace_path"`
	Config        configJSON  `json:"config"`
	CreatedAt     time.Time   `json:"created_at"`
	AgentStarted  bool        `json:"agent_started"`
}

type configJSON struct {
	Agent           string          `json:"agent"`
	Template        string          `json:"template,omitempty"`
	AllowWritePaths []string        `json:"allow_write_paths"`
	DenyWritePaths  []string        `json:"deny_write_paths"`
	NetworkEnabled  bool            `json:"network_enabled"`
	AllowDomains    []string        `json:"allow_domains,omitempty"`
	TimeoutMinutes  int             `json:"timeout_minutes"`
	PortMappings    map[int]int     `json:"port_mappings,omitempty"`
}

func (s *Store) Save(sbx *Sandbox) error {
	dir := filepath.Join(s.root, sbx.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(toJSON(sbx), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0o640)
}

func (s *Store) Load(id string) (*Sandbox, error) {
	path := filepath.Join(s.root, id, "state.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("sandbox %q: not found", id)
	}
	if err != nil {
		return nil, err
	}
	var j sandboxJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("sandbox %q: corrupt state: %w", id, err)
	}
	return fromJSON(j), nil
}

func (s *Store) Delete(id string) error {
	path := filepath.Join(s.root, id, "state.json")
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) List() ([]*Sandbox, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Sandbox
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sbx, err := s.Load(e.Name())
		if err != nil {
			continue // skip corrupt entries
		}
		out = append(out, sbx)
	}
	return out, nil
}

func (s *Store) WorkspaceDir(id string) string {
	return filepath.Join(s.root, id, "workspace")
}

func toJSON(sbx *Sandbox) sandboxJSON {
	return sandboxJSON{
		ID:            sbx.ID,
		State:         string(sbx.State),
		WorkspacePath: sbx.WorkspacePath,
		CreatedAt:     sbx.CreatedAt,
		AgentStarted:  sbx.AgentStarted,
		Config: configJSON{
			Agent:           sbx.Config.Agent,
			Template:        sbx.Config.Template,
			AllowWritePaths: sbx.Config.Guardrails.AllowWritePaths,
			DenyWritePaths:  sbx.Config.Guardrails.DenyWritePaths,
			NetworkEnabled:  sbx.Config.Guardrails.Network.Enabled,
			AllowDomains:    sbx.Config.Guardrails.Network.AllowDomains,
			TimeoutMinutes:  sbx.Config.Guardrails.TimeoutMinutes,
			PortMappings:    sbx.Config.PortMappings,
		},
	}
}

func fromJSON(j sandboxJSON) *Sandbox {
	pm := j.Config.PortMappings
	if pm == nil {
		pm = map[int]int{3000: 3000, 8080: 8080}
	}
	return &Sandbox{
		ID:            j.ID,
		State:         State(j.State),
		WorkspacePath: j.WorkspacePath,
		CreatedAt:     j.CreatedAt,
		AgentStarted:  j.AgentStarted,
		Config: Config{
			Agent:    j.Config.Agent,
			Template: j.Config.Template,
			Guardrails: Guardrails{
				Network: NetworkConfig{
					Enabled:      j.Config.NetworkEnabled,
					AllowDomains: j.Config.AllowDomains,
				},
				AllowWritePaths: j.Config.AllowWritePaths,
				DenyWritePaths:  j.Config.DenyWritePaths,
				TimeoutMinutes:  j.Config.TimeoutMinutes,
			},
			PortMappings: pm,
		},
	}
}
