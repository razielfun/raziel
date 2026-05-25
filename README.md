# Raziel

Open source platform for running AI coding agents in isolated sandboxes and deploying their work to live HTTPS URLs.

Agents get a workspace, full tool access, and a deployment pipeline — all in a single static binary. No containers, no Redis, no Postgres required to get started.

```
raziel server          # start API + worker
raziel deploy .        # package and deploy to Fly.io
raziel list            # list deployments (JSON)
raziel sandbox run     # run a command in an isolated local sandbox
```

---

## What it does

- **Local sandboxes** — OS-level isolation on macOS (seatbelt) and Linux (bubblewrap). Agents run with a restricted filesystem and optional network controls. State persists across restarts.
- **Cloud deployments** — ships agent-built code to Fly.io as live HTTPS endpoints. Supports zero-downtime redeployments, config-only updates, and custom domains.
- **HTTP API** — REST API that any frontend or agent can drive. JSON-first, CORS enabled, bearer token auth.
- **Single binary** — API server, background worker, and CLI in one `raziel` executable. SQLite by default, no external services needed.

---

## Quickstart

### Install

```bash
# macOS
brew install go  # requires Go 1.23+
git clone https://github.com/razielfun/raziel.git
cd raziel
make build

# Or download a release binary (coming soon)
```

### Run the server

```bash
export RAZIEL_API_SECRET=your-secret-here
export FLY_API_TOKEN=your-fly-token       # optional, needed for cloud deploys
export FLY_ORG=your-fly-org               # optional

./raziel server
# → listening on 0.0.0.0:8000
```

### Deploy a project

Create a `raziel.yaml` in your project:

```yaml
name: my-api
template: backend-service
runtime: python
port: 8080
health_path: /health
tier: starter
```

Then deploy:

```bash
export RAZIEL_API_SECRET=your-secret-here

./raziel deploy ./my-project
# Packages directory, uploads to server, waits for ready
# → { "deployment_id": "dep_a1b2c3d4", "state": "ready", "url": "https://my-api.fly.dev" }
```

### Local sandbox

Run a command in an isolated workspace:

```bash
./raziel sandbox create sbx_01
./raziel sandbox run sbx_01 -- /bin/bash -c "echo hello from sandbox"
./raziel sandbox list
./raziel sandbox destroy sbx_01
```

---

## Configuration

All config is via environment variables. Only `RAZIEL_API_SECRET` is required.

| Variable | Default | Description |
|---|---|---|
| `RAZIEL_API_SECRET` | — | **Required.** Bearer token for the API |
| `RAZIEL_PORT` | `8000` | HTTP listen port |
| `RAZIEL_HOST` | `0.0.0.0` | HTTP listen address |
| `RAZIEL_DATABASE_URL` | `raziel.db` | SQLite file path (or Postgres DSN) |
| `RAZIEL_STORAGE_PATH` | `.raziel/artifacts` | Artifact storage directory |
| `RAZIEL_WORKER_CONCURRENCY` | `4` | Parallel deploy jobs |
| `RAZIEL_BUILD_TIMEOUT` | `10m` | Max build duration |
| `RAZIEL_DEPLOY_TIMEOUT` | `10m` | Max deploy duration |
| `RAZIEL_DEBUG` | `false` | Verbose structured logging |
| `FLY_API_TOKEN` | — | Fly.io API token (required for cloud deploys) |
| `FLY_ORG` | — | Fly.io org slug |

---

## REST API

All endpoints require `Authorization: Bearer <RAZIEL_API_SECRET>` except `/health`.

### Deployments

```
GET    /health                             — liveness check (no auth)
GET    /me                                 — auth context

POST   /v0/deployments                     — create deployment
GET    /v0/deployments                     — list (supports ?name=&state=&limit=&offset=)
GET    /v0/deployments/{id}               — get one
DELETE /v0/deployments/{id}               — destroy

GET    /v0/deployments/{id}/logs          — build/deploy logs (?type=build|deploy|runtime)
POST   /v0/deployments/{id}/domains       — add custom domain  { "hostname": "..." }
DELETE /v0/deployments/{id}/domains/{h}   — remove custom domain
```

### Create deployment (multipart/form-data)

```
POST /v0/deployments
  manifest  — raziel.yaml file
  artifact  — directory as .tar.gz
  secrets   — JSON object of KEY=VALUE secrets (optional)
  previous_deployment_id — redeploy from this ID (optional)
```

Supports `Idempotency-Key` header for safe retries.

### Deployment object

```json
{
  "deployment_id": "dep_a1b2c3d4e5f6g7h8",
  "name": "my-api",
  "state": "ready",
  "url": "https://raziel-a1b2c3d4e5f6.fly.dev",
  "version": 1,
  "is_latest": true,
  "created_at": "2026-05-25T12:00:00Z",
  "updated_at": "2026-05-25T12:02:30Z",
  "ready_at": "2026-05-25T12:02:30Z"
}
```

### Deployment states

```
queued → building → deploying → ready
   ↘        ↘           ↘
      failed ←────────────┘
        ↓
    destroyed
```

### Error format

```json
{
  "error": "build failed: requirements.txt not found",
  "code": "BUILD_FAILED",
  "recovery_hint": "Check the build logs for details"
}
```

---

## Manifest (raziel.yaml)

```yaml
name: my-service          # required, lowercase letters/numbers/hyphens
template: backend-service # backend-service | static-site | web-app | docker
runtime: python           # python | node | fullstack (not needed for docker)
port: 8080                # port your app listens on (default: 8080)
health_path: /health      # health check path (default: /health)
tier: starter             # starter | performance | pro

# Environment variable schema (declarative, enforced at deploy time)
env_schema:
  - name: DATABASE_URL
    type: url
    required: true
    secret: true
    description: PostgreSQL connection string

# Persistent volumes
volumes:
  - name: data
    path: /data
    size_gb: 10

# Optional features
features:
  database: false
  auth: false
```

### Machine tiers

| Tier | CPUs | Memory | Notes |
|---|---|---|---|
| `starter` | 2 shared | 2 GB | Auto-stop after 5min idle |
| `performance` | 4 shared | 4 GB | Auto-stop after 5min idle |
| `pro` | 4 shared | 8 GB | Auto-stop after 5min idle |

---

## CLI reference

```
raziel server                        start API server + worker
raziel deploy [dir]                  package dir and deploy
  --previous <id>                    redeploy from a previous deployment
  --secret KEY=VALUE                 inject secret (repeatable)
  --wait=false                       return immediately without polling
raziel list                          list all deployments
raziel get <id>                      get one deployment
raziel logs <id>                     get build/deploy logs
  --type build|deploy|runtime
raziel destroy <id>                  destroy a deployment
raziel sandbox create <id>           create a local sandbox
raziel sandbox run <id> -- <cmd>     run command in sandbox
raziel sandbox list                  list sandboxes
raziel sandbox stop <id>             stop sandbox (keeps workspace)
raziel sandbox destroy <id>          destroy sandbox and workspace
```

---

## Local sandbox

Sandboxes provide OS-level process isolation for AI agents.

**macOS** — uses `sandbox-exec` (seatbelt). A generated SBPL policy restricts writes outside the workspace and can block outbound network.

**Linux** — uses `bwrap` (bubblewrap). Mounts system paths read-only, provides a private `/proc`, `/dev`, and `/tmp`. Optional network namespace isolation.

Sandbox state persists to `~/.raziel/sandboxes/{id}/state.json`. Workspace files survive process restarts. Only `destroy` removes them.

```bash
# Create a sandbox
./raziel sandbox create sbx_myagent

# Run a command inside it (isolated)
./raziel sandbox run sbx_myagent -- python3 -c "print('hello')"

# The workspace is persistent — files written here survive
./raziel sandbox run sbx_myagent -- /bin/sh -c "echo 'data' > output.txt"
./raziel sandbox run sbx_myagent -- /bin/cat output.txt
# → data

# Stop (pause) without deleting files
./raziel sandbox stop sbx_myagent

# Clean up everything
./raziel sandbox destroy sbx_myagent
```

---

## Build from source

```bash
git clone https://github.com/razielfun/raziel.git
cd raziel
make build          # produces ./raziel binary (CGO_ENABLED=0, fully static)
make test           # run all tests
make build-all      # cross-compile linux/darwin/windows
```

Requires Go 1.23+. No CGO. The binary embeds SQLite (via `modernc.org/sqlite`) — no system libraries needed.

---

## Architecture

```
raziel server
├── HTTP API (chi router)          :8000
│   ├── POST /v0/deployments       multipart upload → enqueue job
│   ├── GET  /v0/deployments       list (tenant-scoped)
│   ├── GET  /v0/deployments/{id}  get + status
│   └── DELETE /v0/deployments/{id} destroy
│
├── Worker pool (goroutines)
│   └── DeployJob
│       ├── Extract artifact
│       ├── flyctl deploy --remote-only  (subprocess)
│       ├── Health check polling
│       └── State transitions (CAS updates)
│
└── SQLite database (WAL mode)
    ├── deployments
    ├── provider_resources
    ├── build_logs
    └── idempotency_keys
```

```
raziel sandbox
├── Store  ~/.raziel/sandboxes/{id}/state.json
└── Provider
    ├── seatbelt.go  (darwin)  sandbox-exec + SBPL policy
    └── bubblewrap.go (linux)  bwrap namespace isolation
```

---

## License

MIT — see [LICENSE](LICENSE).
