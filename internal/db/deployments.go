package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("not found")

func (d *DB) CreateDeployment(dep *Deployment) error {
	_, err := d.sql.Exec(`
		INSERT INTO deployments
			(id, deployment_id, tenant_id, owner_id, api_key_id, name, state,
			 artifact_key, manifest_json, src_hash, config_only, version, is_latest,
			 previous_deployment_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		dep.ID, dep.DeploymentID, dep.TenantID, dep.OwnerID, dep.APIKeyID,
		dep.Name, dep.State, dep.ArtifactKey, dep.ManifestJSON, dep.SrcHash,
		boolInt(dep.ConfigOnly), dep.Version, boolInt(dep.IsLatest),
		dep.PreviousDeploymentID,
	)
	return err
}

func (d *DB) GetDeployment(deploymentID string) (*Deployment, error) {
	row := d.sql.QueryRow(`
		SELECT id, deployment_id, tenant_id, owner_id, api_key_id, name, state,
		       artifact_key, manifest_json, error_message, recovery_hint, url,
		       version, is_latest, previous_deployment_id, src_hash, config_only,
		       expires_at, ready_at, created_at, updated_at
		FROM deployments WHERE deployment_id = ?`, deploymentID)
	return scanDeployment(row)
}

func (d *DB) GetDeploymentByTenant(tenantID, deploymentID string) (*Deployment, error) {
	row := d.sql.QueryRow(`
		SELECT id, deployment_id, tenant_id, owner_id, api_key_id, name, state,
		       artifact_key, manifest_json, error_message, recovery_hint, url,
		       version, is_latest, previous_deployment_id, src_hash, config_only,
		       expires_at, ready_at, created_at, updated_at
		FROM deployments WHERE deployment_id = ? AND tenant_id = ?`,
		deploymentID, tenantID)
	return scanDeployment(row)
}

type ListDeploymentsOpts struct {
	TenantID string
	Name     string
	State    string
	Limit    int
	Offset   int
}

func (d *DB) ListDeployments(opts ListDeploymentsOpts) ([]*Deployment, error) {
	if opts.Limit == 0 {
		opts.Limit = 50
	}
	rows, err := d.sql.Query(`
		SELECT id, deployment_id, tenant_id, owner_id, api_key_id, name, state,
		       artifact_key, manifest_json, error_message, recovery_hint, url,
		       version, is_latest, previous_deployment_id, src_hash, config_only,
		       expires_at, ready_at, created_at, updated_at
		FROM deployments
		WHERE tenant_id = ?
		  AND (? = '' OR name = ?)
		  AND (? = '' OR state = ?)
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`,
		opts.TenantID,
		opts.Name, opts.Name,
		opts.State, opts.State,
		opts.Limit, opts.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Deployment
	for rows.Next() {
		dep, err := scanDeploymentRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

// TransitionState does a CAS update: only succeeds if current state matches fromState.
// Returns ErrNotFound if the row doesn't exist or the state doesn't match (race).
func (d *DB) TransitionState(deploymentID, fromState, toState string, opts ...TransitionOpt) error {
	cfg := &transitionCfg{}
	for _, o := range opts {
		o(cfg)
	}
	res, err := d.sql.Exec(`
		UPDATE deployments
		SET state = ?, error_message = ?, recovery_hint = ?, url = ?,
		    ready_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE deployment_id = ? AND state = ?`,
		toState, cfg.errMsg, cfg.hint, cfg.url,
		timePtr(cfg.readyAt),
		deploymentID, fromState,
	)
	if err != nil {
		return fmt.Errorf("transition %s → %s: %w", fromState, toState, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("transition %s → %s: %w (state may have changed)", fromState, toState, ErrNotFound)
	}
	return nil
}

func (d *DB) SetNotLatest(deploymentID string) error {
	_, err := d.sql.Exec(`
		UPDATE deployments SET is_latest = 0, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE deployment_id = ?`, deploymentID)
	return err
}

type transitionCfg struct {
	errMsg  string
	hint    string
	url     string
	readyAt *time.Time
}

type TransitionOpt func(*transitionCfg)

func WithError(msg, hint string) TransitionOpt {
	return func(c *transitionCfg) { c.errMsg = msg; c.hint = hint }
}

func WithURL(url string) TransitionOpt {
	return func(c *transitionCfg) { c.url = url }
}

func WithReadyAt(t time.Time) TransitionOpt {
	return func(c *transitionCfg) { c.readyAt = &t }
}

func scanDeployment(row *sql.Row) (*Deployment, error) {
	dep := &Deployment{}
	var expiresAt, readyAt, createdAt, updatedAt sql.NullString
	var isLatest, configOnly int
	err := row.Scan(
		&dep.ID, &dep.DeploymentID, &dep.TenantID, &dep.OwnerID, &dep.APIKeyID,
		&dep.Name, &dep.State, &dep.ArtifactKey, &dep.ManifestJSON,
		&dep.ErrorMessage, &dep.RecoveryHint, &dep.URL,
		&dep.Version, &isLatest, &dep.PreviousDeploymentID, &dep.SrcHash, &configOnly,
		&expiresAt, &readyAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dep.IsLatest = isLatest == 1
	dep.ConfigOnly = configOnly == 1
	dep.CreatedAt = parseTime(createdAt.String)
	dep.UpdatedAt = parseTime(updatedAt.String)
	if readyAt.Valid {
		t := parseTime(readyAt.String)
		dep.ReadyAt = &t
	}
	if expiresAt.Valid {
		t := parseTime(expiresAt.String)
		dep.ExpiresAt = &t
	}
	return dep, nil
}

func scanDeploymentRow(rows *sql.Rows) (*Deployment, error) {
	dep := &Deployment{}
	var expiresAt, readyAt, createdAt, updatedAt sql.NullString
	var isLatest, configOnly int
	err := rows.Scan(
		&dep.ID, &dep.DeploymentID, &dep.TenantID, &dep.OwnerID, &dep.APIKeyID,
		&dep.Name, &dep.State, &dep.ArtifactKey, &dep.ManifestJSON,
		&dep.ErrorMessage, &dep.RecoveryHint, &dep.URL,
		&dep.Version, &isLatest, &dep.PreviousDeploymentID, &dep.SrcHash, &configOnly,
		&expiresAt, &readyAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	dep.IsLatest = isLatest == 1
	dep.ConfigOnly = configOnly == 1
	dep.CreatedAt = parseTime(createdAt.String)
	dep.UpdatedAt = parseTime(updatedAt.String)
	if readyAt.Valid {
		t := parseTime(readyAt.String)
		dep.ReadyAt = &t
	}
	if expiresAt.Valid {
		t := parseTime(expiresAt.String)
		dep.ExpiresAt = &t
	}
	return dep, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func timePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
