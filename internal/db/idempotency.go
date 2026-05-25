package db

import (
	"database/sql"
	"time"
)

func (d *DB) StoreIdempotencyKey(k *IdempotencyKey) error {
	_, err := d.sql.Exec(`
		INSERT OR IGNORE INTO idempotency_keys (id, idem_key, deployment_id, expires_at)
		VALUES (?, ?, ?, ?)`,
		k.ID, k.IdemKey, k.DeploymentID,
		k.ExpiresAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (d *DB) LookupIdempotencyKey(idemKey string) (*IdempotencyKey, error) {
	k := &IdempotencyKey{}
	var createdAt, expiresAt string
	err := d.sql.QueryRow(`
		SELECT id, idem_key, deployment_id, created_at, expires_at
		FROM idempotency_keys
		WHERE idem_key = ? AND expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		idemKey).
		Scan(&k.ID, &k.IdemKey, &k.DeploymentID, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt = parseTime(createdAt)
	k.ExpiresAt = parseTime(expiresAt)
	return k, nil
}

func (d *DB) ExpireIdempotencyKeys() error {
	_, err := d.sql.Exec(`
		DELETE FROM idempotency_keys
		WHERE expires_at <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`)
	return err
}
