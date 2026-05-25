CREATE TABLE IF NOT EXISTS deployments (
    id                    TEXT PRIMARY KEY,
    deployment_id         TEXT NOT NULL UNIQUE,
    tenant_id             TEXT NOT NULL DEFAULT 'default',
    owner_id              TEXT NOT NULL DEFAULT 'default',
    api_key_id            TEXT NOT NULL DEFAULT '',
    name                  TEXT NOT NULL,
    state                 TEXT NOT NULL DEFAULT 'queued',
    artifact_key          TEXT NOT NULL DEFAULT '',
    manifest_json         TEXT NOT NULL DEFAULT '{}',
    error_message         TEXT NOT NULL DEFAULT '',
    recovery_hint         TEXT NOT NULL DEFAULT '',
    url                   TEXT NOT NULL DEFAULT '',
    version               INTEGER NOT NULL DEFAULT 1,
    is_latest             INTEGER NOT NULL DEFAULT 1,
    previous_deployment_id TEXT NOT NULL DEFAULT '',
    src_hash              TEXT NOT NULL DEFAULT '',
    config_only           INTEGER NOT NULL DEFAULT 0,
    expires_at            TEXT,
    ready_at              TEXT,
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_deployments_tenant_name ON deployments(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_deployments_state ON deployments(state);

CREATE TABLE IF NOT EXISTS provider_resources (
    id             TEXT PRIMARY KEY,
    deployment_id  TEXT NOT NULL UNIQUE,
    provider       TEXT NOT NULL DEFAULT 'fly',
    app_name       TEXT NOT NULL DEFAULT '',
    machine_id     TEXT NOT NULL DEFAULT '',
    region         TEXT NOT NULL DEFAULT '',
    image_ref      TEXT NOT NULL DEFAULT '',
    image_label    TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (deployment_id) REFERENCES deployments(deployment_id)
);

CREATE TABLE IF NOT EXISTS build_logs (
    id             TEXT PRIMARY KEY,
    deployment_id  TEXT NOT NULL,
    log_type       TEXT NOT NULL DEFAULT 'build',
    content        TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (deployment_id) REFERENCES deployments(deployment_id)
);

CREATE INDEX IF NOT EXISTS idx_build_logs_deployment ON build_logs(deployment_id, log_type);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    id             TEXT PRIMARY KEY,
    idem_key       TEXT NOT NULL UNIQUE,
    deployment_id  TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL DEFAULT 'default',
    principal_id    TEXT NOT NULL DEFAULT 'default',
    key_prefix      TEXT NOT NULL UNIQUE,
    key_hash        TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    scopes          TEXT NOT NULL DEFAULT 'read,deploy,delete,admin',
    is_revoked      INTEGER NOT NULL DEFAULT 0,
    last_used_at    TEXT,
    expires_at      TEXT,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
