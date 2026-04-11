# AgentOps Core

[![CI](https://github.com/samyn92/agentops-core/actions/workflows/ci.yaml/badge.svg)](https://github.com/samyn92/agentops-core/actions/workflows/ci.yaml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-%E2%89%A51.28-326CE5.svg)](https://kubernetes.io/)

Kubernetes operator for deploying AI agents as native workloads. Define agents, tools, channels, and resources as Custom Resources — the operator handles deployments, jobs, storage, networking, MCP gateway sidecars, channel bridges, concurrency control, and memory integration.

Agents run the [Charm Fantasy SDK](https://github.com/charmbracelet/fantasy) via the standalone [agentops-runtime](https://github.com/samyn92/agentops-runtime).

---

## Table of Contents

- [Architecture](#architecture)
- [Features](#features)
- [Custom Resource Definitions](#custom-resource-definitions)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Installation](#installation)
- [Usage Examples](#usage-examples)
- [Configuration](#configuration)
- [MCP Gateway](#mcp-gateway)
- [Memory](#memory)
- [Webhooks](#webhooks)
- [Project Structure](#project-structure)
- [Development](#development)
- [CI/CD](#cicd)
- [Images](#images)
- [Related Projects](#related-projects)
- [Contributing](#contributing)
- [License](#license)

---

## Architecture

<picture>
  <img alt="agentops-core architecture" src="docs/architecture.svg" width="100%">
</picture>

<details>
<summary>Data flow summary</summary>

| Source | Target Agent Mode | Path |
|--------|:-----------------:|------|
| Chat channel (Telegram, Slack, Discord) | daemon | Channel bridge &rarr; HTTP POST &rarr; Agent Service |
| Event channel (GitLab, GitHub, Webhook) | daemon or task | Channel bridge &rarr; AgentRun CR &rarr; Reconciler |
| `run_agent` tool (agent-to-agent) | daemon or task | Daemon agent &rarr; AgentRun CR &rarr; Reconciler |
| Cron schedule | daemon or task | Operator &rarr; AgentRun CR &rarr; Reconciler |
| Console (web UI) | daemon | HTTP &rarr; Agent Service (:4096) |

</details>

---

## Features

- **Two agent modes** — long-running daemons (Deployment + PVC + Service) or one-shot task Jobs
- **Unified tool catalog** — deliver tools via OCI artifacts, MCP servers, ConfigMaps, inline scripts, or skills
- **MCP gateway** — sidecar proxy in agent pods enforcing per-agent deny/allow permission rules
- **Channel bridges** — connect Telegram, Slack, Discord, GitLab, GitHub, and generic webhooks to agents
- **agentops-memory** — persistent shared memory with FTS5 BM25 relevance-ranked context injection, deterministic session summaries, and system-prompt injection
- **Resource bindings** — declarative catalog of GitHub repos/orgs, GitLab projects/groups, S3 buckets, and documentation URLs
- **Concurrency control** — per-agent run limits with queue, reject, or replace policies
- **Cron scheduling** — trigger agent runs on a schedule with templated prompts
- **Agent-to-agent orchestration** — agents can spawn sub-runs via the `run_agent` tool
- **Validating webhooks** — admission validation for all CRD types
- **Helm chart** — production-ready chart with RBAC, CRDs, metrics, and webhook support
- **Server-Side Apply (SSA)** — all reconcilers use SSA patching with generation-changed predicates

---

## Custom Resource Definitions

Five CRDs in API group `agents.agentops.io/v1alpha1`:

| CRD | Short Name | Description |
|-----|-----------|-------------|
| **Agent** | `ag` | AI agent — model, tools, memory, mode |
| **AgentRun** | `ar` | Single prompt execution against an agent |
| **Channel** | `ch` | Bridge from external platforms to agents |
| **AgentTool** | `agtool` | Unified tool catalog — OCI, MCP, inline, skill |
| **AgentResource** | `ares` | External resource catalog (repos, S3, docs) |

### Agent

Two modes:

- **`daemon`** — long-running Deployment + PVC + Service. Receives prompts via HTTP, maintains conversation state, persists knowledge to agentops-memory.
- **`task`** — one-shot Job per AgentRun. Prompt in, structured result out, container exits.

Key spec fields:

| Group | Fields |
|-------|--------|
| Runtime | `image`, `builtinTools`, `temperature`, `maxOutputTokens`, `maxSteps` |
| Model | `model`, `primaryProvider`, `titleModel`, `providers`, `fallbackModels` |
| Identity | `systemPrompt`, `contextFiles` |
| Tools | `tools` (AgentTool bindings with per-agent permissions), `permissionTools`, `enableQuestionTool` |
| Memory | `memory` (agentops-memory: serverRef, project, contextLimit, windowSize, autoSummarize) |
| Tool Hooks | `toolHooks` (blocked commands, allowed paths, audit tools) |
| Resources | `resourceBindings` (bind AgentResource CRs for context injection) |
| Schedule | `schedule` (cron), `schedulePrompt` |
| Concurrency | `concurrency.maxRuns`, `concurrency.policy` (queue / reject / replace) |
| Storage | `storage` (PVC for daemon agents) |
| Infrastructure | `resources`, `serviceAccountName`, `timeout`, `networkPolicy` |

### AgentRun

Tracks a single execution. Created by channels, cron schedules, or the `run_agent` orchestration tool.

- **Spec**: `agentRef`, `prompt`, `source`, `sourceRef`
- **Status**: `phase`, `output`, `toolCalls`, `model`, `usage`

Concurrency is enforced per-agent: `queue` (FIFO wait), `reject` (fail immediately), `replace` (cancel previous).

### Channel

Bridges external platforms to agents. Two bridge types:

- **Chat** (Telegram, Slack, Discord) — forward messages directly via HTTP POST to daemon agents
- **Event** (GitLab, GitHub, Webhook) — render prompts from templates, create AgentRun CRs

Deployed as Deployments with optional Ingress + TLS (cert-manager).

### AgentTool

Unified tool catalog entry. Six source types:

| Source | Description |
|--------|-------------|
| `oci` | OCI artifact with MCP tool server binary, pulled via crane init container |
| `configMap` | Tool script mounted from a ConfigMap |
| `inline` | Embedded tool content (< 4 KB, prototyping) |
| `mcpServer` | Operator-managed MCP server Deployment + Service |
| `mcpEndpoint` | External MCP endpoint, health-checked by the operator |
| `skill` | OCI-packaged skill markdown (system prompt extensions) |

Agent pods include MCP gateway sidecars in proxy mode that enforce per-agent deny/allow permission rules.

### AgentResource

Declarative catalog of external resources:

| Kind | Config |
|------|--------|
| `github-repo` | owner, repo, defaultBranch, apiURL |
| `github-org` | org, repoFilter, apiURL |
| `gitlab-project` | baseURL, project, defaultBranch |
| `gitlab-group` | baseURL, group, projects filter |
| `git-repo` | url, branch |
| `s3-bucket` | bucket, region, endpoint, prefix |
| `documentation` | urls |

Bound to agents via `spec.resourceBindings` with optional `readOnly` and `autoContext` flags.

---

## Prerequisites

- Kubernetes cluster **>= 1.28** (tested on k3s)
- `kubectl` configured with cluster access
- Go **1.26** (for development)
- [cert-manager](https://cert-manager.io/) (if using webhooks)
- Container runtime (Docker or Podman) for building images

---

## Quick Start

### 1. Install CRDs

```sh
make install
```

### 2. Run the operator locally

```sh
make run
```

With webhooks enabled (requires cert-manager):

```sh
make run ARGS="--enable-webhooks"
```

### 3. Create an agent

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: Agent
metadata:
  name: code-reviewer
  namespace: agents
spec:
  mode: daemon
  model: anthropic/claude-sonnet-4-20250514
  image: ghcr.io/samyn92/agent-runtime-fantasy:latest
  builtinTools: [bash, read, edit, write, grep, glob]
  temperature: 0.3
  maxOutputTokens: 8192
  maxSteps: 50
  providers:
    - name: anthropic
      apiKeySecret:
        name: llm-keys
        key: anthropic-api-key
  tools:
    - name: k8s-helper
  systemPrompt: |
    You are a senior code reviewer. Review PRs thoroughly,
    focusing on correctness, security, and maintainability.
  storage:
    size: 5Gi
```

### 4. Trigger a run

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: AgentRun
metadata:
  name: review-run-001
spec:
  agentRef: code-reviewer
  source: channel
  sourceRef: manual
  prompt: |
    Review the merge request "Add user authentication" in project
    samyn92/my-app. Focus on security issues in the authentication flow.
```

### 5. Apply sample resources

```sh
kubectl apply -k config/samples/
```

---

## Installation

### Helm (recommended)

```sh
# From OCI registry
helm install agentops-operator \
  oci://ghcr.io/samyn92/charts/agentops-operator \
  --namespace agent-system --create-namespace

# From source
helm install agentops-operator charts/agentops-operator \
  --namespace agent-system --create-namespace
```

### Kustomize

```sh
# Build and push the operator image
make docker-build docker-push IMG=ghcr.io/samyn92/agentops-operator:latest

# Deploy to cluster
make deploy IMG=ghcr.io/samyn92/agentops-operator:latest
```

### Consolidated manifest

```sh
make build-installer IMG=ghcr.io/samyn92/agentops-operator:latest
kubectl apply -f dist/install.yaml
```

### Uninstall

```sh
# Helm
helm uninstall agentops-operator -n agent-system

# Kustomize
make undeploy

# CRDs only
make uninstall
```

---

## Usage Examples

### MCP server tool

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: AgentTool
metadata:
  name: gitlab-mcp
spec:
  description: "GitLab MCP server — merge requests, issues, pipelines"
  category: scm
  mcpServer:
    image: ghcr.io/samyn92/mcp-gitlab:latest
    port: 8080
    command: [node, /app/server.js]
    env:
      GITLAB_URL: "https://gitlab.com"
    secrets:
      - name: GITLAB_TOKEN
        secretRef:
          name: gitlab-credentials
          key: token
    healthCheck:
      path: /health
      intervalSeconds: 30
  defaultPermissions:
    mode: allow
    rules:
      - "get_merge_request"
      - "create_merge_request_note"
      - "list_merge_requests"
```

### OCI tool

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: AgentTool
metadata:
  name: k8s-helper
spec:
  description: "Kubernetes helper — deploy, inspect, and manage workloads"
  category: infrastructure
  oci:
    ref: ghcr.io/samyn92/agent-tools/k8s-helper:1.0.0
    pullPolicy: IfNotPresent
```

### Telegram channel

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: Channel
metadata:
  name: telegram-chat
spec:
  type: telegram
  agentRef: code-reviewer
  telegram:
    botTokenSecret:
      name: telegram-bot-secret
      key: BOT_TOKEN
    allowedUsers: ["123456789"]
    allowedChats: ["-1001234567890"]
  image: ghcr.io/samyn92/channel-telegram:latest
```

### GitHub resource binding

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: AgentResource
metadata:
  name: agentops-core
  namespace: agents
spec:
  kind: github-repo
  displayName: "AgentOps Core"
  description: "Kubernetes operator for deploying AI agents"
  credentials:
    name: github-tokens
    key: GITHUB_TOKEN
  github:
    owner: samyn92
    repo: agentops-core
    defaultBranch: main
```

More examples in [`config/samples/`](config/samples/).

---

## Configuration

### Operator flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `0` (disabled) | Metrics endpoint (`:8443` for HTTPS, `:8080` for HTTP) |
| `--health-probe-bind-address` | `:8081` | Health/readiness probe endpoint |
| `--leader-elect` | `false` | Enable leader election for HA |
| `--enable-webhooks` | `false` | Enable admission webhooks (requires cert-manager) |
| `--metrics-secure` | `true` | Serve metrics over HTTPS |
| `--enable-http2` | `false` | Enable HTTP/2 (disabled by default for security) |
| `--webhook-cert-path` | `""` | Directory containing webhook TLS certificates |
| `--metrics-cert-path` | `""` | Directory containing metrics TLS certificates |

### Helm values

Key Helm values (see [`charts/agentops-operator/values.yaml`](charts/agentops-operator/values.yaml) for the full reference):

| Key | Default | Description |
|-----|---------|-------------|
| `image.repository` | `ghcr.io/samyn92/agentops-operator` | Operator image |
| `leaderElection.enabled` | `true` | Enable leader election |
| `webhooks.enabled` | `false` | Enable admission webhooks |
| `metrics.enabled` | `true` | Enable metrics endpoint |
| `metrics.secure` | `true` | Serve metrics over HTTPS |
| `installCRDs` | `true` | Install CRDs with the chart |
| `agentNamespace` | `agents` | Namespace for agent workloads |

### Namespaces

| Namespace | Purpose |
|-----------|---------|
| `agent-system` | Operator, console, dev pod |
| `agents` | Agent workloads (created when deploying CRs) |

---

## MCP Gateway

Custom Go binary in `images/mcp-gateway/` with two modes:

- **Spawn mode** — wraps an MCP server subprocess (stdio JSON-RPC) behind HTTP+SSE. Used by AgentTool `mcpServer` Deployments.
- **Proxy mode** — sidecar in agent pods that forwards MCP requests to upstream servers while enforcing per-agent deny/allow permission rules on `tools/call` requests.

---

## Memory

Agents can use [agentops-memory](https://github.com/samyn92/agentops-memory) for persistent shared memory. Configure via `spec.memory`:

```yaml
spec:
  memory:
    serverRef: agentops-memory  # AgentTool CR name or in-cluster service
    project: my-agent          # defaults to agent name
    contextLimit: 5
    windowSize: 20
    autoSummarize: true
```

The operator resolves the memory server URL by:

1. Looking up a matching AgentTool CR by `serverRef` name (mcpServer/mcpEndpoint sources)
2. Falling back to in-cluster service DNS (`http://<serverRef>.<namespace>.svc:7437`)

Configuration is injected into `/etc/operator/config.json` and consumed by the runtime's memory client.

---

## Webhooks

Four validating webhooks (non-mutating, `failurePolicy: Fail`):

| Webhook | Validates |
|---------|-----------|
| **Agent** | Mode, model format, provider references, tool bindings, concurrency policy, cron syntax |
| **Channel** | Platform type, chat vs event mode, required fields per platform, template syntax |
| **AgentTool** | Exactly one source block set, source-specific fields, deny/allow mode on MCP sources only |
| **AgentResource** | Kind, kind-specific required fields, URL formats |

Enable with `--enable-webhooks` (requires cert-manager for TLS certificates).

---

## Project Structure

```
agentops-core/
  api/v1alpha1/              # CRD types + webhooks
    agent_types.go           #   Agent
    agentrun_types.go        #   AgentRun
    channel_types.go         #   Channel
    agenttool_types.go       #   AgentTool
    agentresource_types.go   #   AgentResource
    shared_types.go          #   MemorySpec, common types
    *_webhook.go             #   Validating webhooks
  cmd/main.go                # Operator entrypoint
  internal/
    controller/              # 5 reconcilers (SSA, generation-changed predicates)
    resources/               # Kubernetes resource builders + tests
  images/
    mcp-gateway/             # MCP gateway binary (spawn + proxy modes)
  config/
    crd/bases/               # Generated CRD manifests
    rbac/                    # Generated RBAC
    manager/                 # Operator Deployment
    webhook/                 # Webhook configuration
    samples/                 # Example CRs
  charts/
    agentops-operator/       # Helm chart
  hack/
    dev/                     # Dev pod manifest + init script
  .github/
    workflows/               # CI + Release pipelines
  Dockerfile                 # Multi-stage build (golang -> distroless)
  Makefile                   # Build, generate, test, deploy targets
```

---

## Development

### Dev pod (recommended)

The dev pod runs in-cluster on k3s with source code mounted via hostPath:

```sh
kubectl apply -f hack/dev/dev-pod.yaml
kubectl exec -it -n agent-system deploy/agentops-dev -- bash

# Inside the pod:
make generate       # Regenerate DeepCopy methods
make manifests      # Regenerate CRD + RBAC + Webhook manifests
make install        # Apply CRDs to the cluster
make run            # Run the operator against k3s
```

### Local development

```sh
make generate       # Generate DeepCopy methods
make manifests      # Generate CRD, RBAC, Webhook manifests
go build ./...      # Build check
make test           # Run tests (envtest)
make lint           # Run golangci-lint
make lint-fix       # Lint with auto-fix
```

### Build targets

| Target | Description |
|--------|-------------|
| `make build` | Build the operator binary |
| `make run` | Run the operator locally |
| `make test` | Run unit tests (envtest) |
| `make lint` | Run golangci-lint |
| `make install` | Apply CRDs to the cluster |
| `make deploy` | Deploy operator to the cluster |
| `make docker-build` | Build the operator container image |
| `make docker-push` | Push the operator container image |
| `make build-installer` | Generate consolidated install manifest |
| `make manifests` | Regenerate CRD + RBAC manifests |
| `make generate` | Regenerate DeepCopy methods |

---

## CI/CD

### CI (`ci.yaml`)

Runs on push to `main` and pull requests:

- **Operator**: lint (golangci-lint), test (envtest), build
- **MCP Gateway**: build, test, vet
- **Helm Chart**: lint, template
- **Docker**: build and push operator + gateway images (main branch only)

### Release (`release.yaml`)

Triggered by `v*` tags:

- Lint, test, and build operator + gateway
- Build and push semver-tagged Docker images to GHCR
- Package and push Helm chart to GHCR OCI registry
- Create GitHub Release with CRD manifests and Helm chart artifact

---

## Images

| Image | Source | Purpose |
|-------|--------|---------|
| `ghcr.io/samyn92/agentops-operator` | [`Dockerfile`](Dockerfile) | Kubernetes operator |
| `ghcr.io/samyn92/agentops-runtime` | [agentops-runtime](https://github.com/samyn92/agentops-runtime) | Agent runtime (Fantasy SDK) |
| `ghcr.io/samyn92/mcp-gateway` | [`images/mcp-gateway/`](images/mcp-gateway/) | MCP protocol gateway |
| `ghcr.io/samyn92/agentops-memory` | [agentops-memory](https://github.com/samyn92/agentops-memory) | Purpose-built memory service |

All images are published to GitHub Container Registry (GHCR). The operator and gateway use multi-stage builds with distroless base images.

---

## Related Projects

| Repository | Description |
|------------|-------------|
| [agentops-runtime](https://github.com/samyn92/agentops-runtime) | Agent runtime (Fantasy SDK + agentops-memory) |
| [agentops-console](https://github.com/samyn92/agentops-console) | Web console (Go BFF + SolidJS PWA) |
| [agent-channels](https://github.com/samyn92/agent-channels) | Channel bridge images (Telegram, Slack, GitLab, etc.) |
| [agent-tools](https://github.com/samyn92/agent-tools) | OCI tool/agent packaging CLI + tool packages |
| [agentops-memory](https://github.com/samyn92/agentops-memory) | Purpose-built memory service (SQLite + FTS5 BM25) |
| [Charm Fantasy SDK](https://github.com/charmbracelet/fantasy) | AI agent framework |

---

## Contributing

Contributions are welcome. To get started:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Set up the [dev pod](#dev-pod-recommended) or local development environment
4. Make your changes and ensure tests pass (`make test && make lint`)
5. Commit your changes (`git commit -m "Add my feature"`)
6. Push to your fork (`git push origin feature/my-feature`)
7. Open a Pull Request

Please ensure:
- Code passes `make lint` and `make test`
- CRD changes include `make generate && make manifests`
- New features include tests in `internal/resources/`

---

## License

Apache 2.0
