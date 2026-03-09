# tiphys

A Go service that listens for GitHub webhook events and automatically submits [Argo Workflows](https://argoproj.github.io/argo-workflows/) CI pipelines based on pipeline definitions stored in the target repository.

## How It Works

```
GitHub Push / PR Event
        │
        ▼
 Webhook Service (this)
  ├── Validates HMAC-SHA256 signature
  ├── Parses event (push or pull_request)
  ├── Fetches .argo/workflow.yaml from the repo at the event's commit SHA
  ├── Validates the workflow definition
  └── Submits the Workflow to Argo with runtime parameters injected
        │
        ▼
   Argo Workflows (Kubernetes)
```

## Project Structure

```
├── cmd/server/         # Main entrypoint
├── internal/
│   ├── config/         # Environment-based configuration
│   ├── webhook/        # HTTP handler + GitHub payload types
│   ├── github/         # GitHub API client (fetch pipeline files)
│   ├── argo/           # Argo Workflows client (submit workflows)
│   └── pipeline/       # Workflow YAML loader + validator
├── deploy/k8s/         # Kubernetes manifests
├── examples/.argo/     # Example pipeline definition for target repos
├── Dockerfile
└── Makefile
```

## Prerequisites

- Go 1.22+
- A Kubernetes cluster with [Argo Workflows](https://argoproj.github.io/argo-workflows/installation/) installed
- A GitHub Personal Access Token with `repo` (read) scope
- A GitHub Webhook configured on your repo

## Getting Started

### 1. Configure environment variables

```bash
cp .env.example .env
# Edit .env with your values
```

| Variable         | Required | Default  | Description                                              |
|------------------|----------|----------|----------------------------------------------------------|
| `GITHUB_TOKEN`   | ✅       | —        | GitHub PAT with `repo` read access                       |
| `WEBHOOK_SECRET` | ✅       | —        | Secret set in the GitHub webhook configuration           |
| `PORT`           |          | `8080`   | HTTP server port                                         |
| `ARGO_NAMESPACE` |          | `argo`   | Kubernetes namespace to submit workflows into            |
| `PIPELINE_DIR`   |          | `.argo`  | Directory in the repo containing pipeline YAML           |
| `ALLOWED_REPOS`  |          | (all)    | Comma-separated `org/repo` allowlist; empty = allow all  |
| `LOG_LEVEL`      |          | `info`   | `debug`, `info`, `warn`, `error`                         |
| `KUBECONFIG`     |          | (in-cluster) | Path to kubeconfig for local dev                     |

### 2. Add a pipeline definition to your target repo

Create `.argo/workflow.yaml` in the repository you want to run CI for.
See `examples/.argo/workflow.yaml` for a complete annotated example.

The following parameters are automatically injected at runtime:

| Parameter     | Description                          |
|---------------|--------------------------------------|
| `repo`        | `org/repo` full name                 |
| `commit`      | Full commit SHA                      |
| `branch`      | Short branch name (e.g. `main`)      |
| `ref`         | Full git ref (e.g. `refs/heads/main`)|
| `clone_url`   | HTTPS clone URL                      |
| `event_type`  | `push` or `pull_request`             |
| `pr_number`   | PR number (PR events only)           |
| `pr_action`   | `opened`, `reopened`, `synchronize`  |

### 3. Build and run locally

```bash
# Install dependencies
go mod download

# Run (reads from .env or environment)
export $(grep -v '^#' .env | xargs)
make run

# Or build the binary
make build
./bin/webhook-server
```

### 4. Deploy to Kubernetes

```bash
# 1. Create secrets
kubectl create secret generic webhook-secrets \
  --from-literal=github-token=<PAT> \
  --from-literal=webhook-secret=<WEBHOOK_SECRET> \
  -n argo-webhook

# 2. Update the image in deploy/k8s/deployment.yaml
# 3. Update the ingress hostname

# 4. Build and push your image
make docker-build docker-push IMAGE_NAME=yourregistry/github-argo-webhook

# 5. Apply manifests
make deploy
```

### 5. Configure the GitHub Webhook

1. Go to your repo → **Settings → Webhooks → Add webhook**
2. **Payload URL**: `https://webhook.your-domain.com/webhook`
3. **Content type**: `application/json`
4. **Secret**: the value you set for `WEBHOOK_SECRET`
5. **Events**: Select **Push** and **Pull requests** (or "Send me everything")

## Development

```bash
# Run tests
make test

# Lint
make lint

# Tidy modules
make tidy
```

## Endpoints

| Method | Path       | Description                              |
|--------|-----------|------------------------------------------|
| `POST` | `/webhook` | GitHub webhook receiver                  |
| `GET`  | `/healthz` | Liveness probe                           |
| `GET`  | `/readyz`  | Readiness probe                          |

## Security

- All incoming requests are validated against the HMAC-SHA256 signature provided by GitHub in `X-Hub-Signature-256`.
- Optionally restrict which repos can trigger workflows using `ALLOWED_REPOS`.
- The service runs as a non-root user (`65534`) in the Docker image.
- Kubernetes deployment sets `readOnlyRootFilesystem` and drops all capabilities.

## License

MIT
