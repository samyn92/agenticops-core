# agentops-core

Kubernetes operator for running AI agents as native Kubernetes workloads. The operator manages the full lifecycle of agents — deployments, jobs, storage, MCP server bindings, channel bridges, and concurrency control. Agents run the [Fantasy](https://github.com/charmbracelet/fantasy) SDK (Go) inside containers, with the runtime maintained in the separate [`agentops-runtime-fantasy`](https://github.com/samyn92/agentops-runtime-fantasy) repo.

## Architecture

```
EXTERNAL                       OPERATOR (reconciles)                     KUBERNETES
                                                                        
  Telegram ─┐                 ┌──────────────────┐                       
  Slack    ──┤  Channel CRs   │  Channel Bridge   │  HTTP POST /prompt   
  Discord  ──┤──────────────► │  (Deployment)     │─────────────────────► Agent (daemon)
             │  chat types    │                   │                       Deployment + PVC + Service
             │  forward msgs  └──────────────────┘                       Fantasy SDK + HTTP server (:4096)
             │  directly                                                  ├── /prompt
  GitLab  ───┤                                                            ├── /prompt/stream
  GitHub  ───┤  event types   ┌──────────────────┐                       ├── /steer, /followup, /abort
  Webhook ───┘──────────────► │  Channel Bridge   │─── creates ──► AgentRun CR
                              │  (renders prompt  │                  │
                              │   from template)  │                  │
                              └──────────────────┘                  │
                                                                    │
  run_agent tool ──────────────────── creates ─────────────────► AgentRun CR
  (daemon agent calling another)                                    │
                                                                    │
  Cron Schedule ───────────────────── creates ─────────────────► AgentRun CR
                                                                    │
                                                                    ▼
                                                        ┌──────────────────┐
                                                        │ AgentRunReconciler│
                                                        │                  │
                                                        │ task agent?      │
                                                        │   → create Job   │──► Agent (task)
                                                        │                  │    Job (one-shot)
                                                        │ daemon agent?    │    Fantasy SDK, exits
                                                        │   → HTTP POST    │──► Agent (daemon)
                                                        │     /prompt      │    (already running)
                                                        └──────────────────┘

  MCPServer CRs                ┌──────────────────┐
  (deploy mode)  ────────────► │  MCP Deployment   │  :8080 (mcp-gateway spawn mode)
                               │  + Service        │◄──── SSE ──── gw sidecar in Agent pod
  (external mode) ─────────────│  health probe     │               (proxy mode, deny/allow rules)
                               └──────────────────┘
```

### Data flow

| Source | Target Agent Mode | Path |
|--------|:-----------------:|------|
| Chat channel (Telegram/Slack/Discord) | daemon | Channel bridge -> HTTP POST -> Agent Service |
| Event channel (GitLab/GitHub/Webhook) | daemon or task | Channel bridge -> AgentRun CR -> Reconciler |
| `run_agent` tool | daemon or task | Daemon agent -> AgentRun CR -> Reconciler |
| Cron schedule | daemon or task | Operator -> AgentRun CR -> Reconciler |

## Custom Resource Definitions

| CRD | Short Name | Description |
|-----|-----------|-------------|
| `Agent` | `ag` | Defines an AI agent (model, tools, MCP bindings, mode) |
| `Channel` | `ch` | Bridges external platforms (Telegram, Slack, GitLab, GitHub, etc.) to agents |
| `AgentRun` | `ar` | Tracks a single prompt execution against an agent |
| `MCPServer` | `mcp` | Shared MCP infrastructure (deployed or external) |

### Agent modes

- **`daemon`** — long-running Deployment + PVC + Service. Receives prompts via HTTP (`/prompt`, `/prompt/stream`, `/steer`, `/followup`, `/abort`). Supports session compaction.
- **`task`** — one-shot Job per AgentRun. Prompt in, structured result out, container exits.

### Agent spec highlights

| Field Group | Key Fields |
|-------------|------------|
| Runtime | `image`, `builtinTools`, `temperature`, `maxOutputTokens`, `maxSteps` |
| Model | `model`, `primaryProvider`, `titleModel`, `providers`, `fallbackModels` |
| Identity | `systemPrompt`, `contextFiles` |
| Tools | `toolRefs` (OCI / ConfigMap / inline MCP), `permissionTools`, `enableQuestionTool` |
| MCP Servers | `mcpServers` (shared MCPServer bindings with per-agent permissions) |
| Tool Hooks | `toolHooks` (blocked commands, allowed paths, audit tools) |
| Schedule | `schedule` (cron), `schedulePrompt` |
| Concurrency | `concurrency.maxRuns`, `concurrency.policy` |
| Storage | `storage` (PVC for daemon agents) |
| Infrastructure | `resources`, `serviceAccountName`, `timeout`, `networkPolicy` |

## Project Structure

```
agentops-core/
  api/v1alpha1/              # CRD types (Agent, Channel, AgentRun, MCPServer) + webhooks
  cmd/main.go                # Operator entrypoint (--enable-webhooks flag)
  internal/
    controller/              # 4 reconcilers (agent, agentrun, channel, mcpserver)
    resources/               # Kubernetes resource builders (deployments, jobs, PVCs, etc.)
  images/
    mcp-gateway/             # MCP protocol gateway (spawn + proxy modes)
  config/
    crd/bases/               # Generated CRD YAMLs
    rbac/                    # Generated RBAC
    manager/                 # Operator Deployment manifest
    webhook/                 # Webhook configuration
    samples/                 # Example CRs
  hack/
    dev/                     # Dev pod manifest + init script
  Dockerfile                 # Operator image
  Makefile                   # Build, generate, deploy targets
```

## Related Repos

| Repo | Purpose |
|------|---------|
| [`agentops-runtime-fantasy`](https://github.com/samyn92/agentops-runtime-fantasy) | Fantasy agent runtime (Go, Charm Fantasy SDK) |
| `agent-channels` | Channel bridge images (gitlab, webhook, etc.) |
| `agent-tools` | OCI tool/agent packaging CLI + tool packages |
| `agent-console` | Web console |
| `agent-factory` | Helm chart (future) |

## Prerequisites

- Go 1.26+
- Docker
- kubectl
- Access to a Kubernetes cluster (v1.28+)

## Quick Start

### Install CRDs

```sh
make install
```

### Run the operator locally

```sh
make run
```

Webhooks are disabled by default. To enable them (requires cert-manager or manual TLS):

```sh
make run ARGS="--enable-webhooks"
```

### Deploy to a cluster

```sh
make docker-build docker-push IMG=ghcr.io/samyn92/agentops-core:latest
make deploy IMG=ghcr.io/samyn92/agentops-core:latest
```

### Create a minimal agent

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: Agent
metadata:
  name: my-agent
spec:
  mode: daemon
  model: anthropic/claude-sonnet-4-20250514
  primaryProvider: anthropic
  providers:
    - name: anthropic
      apiKeySecret:
        name: llm-api-keys
        key: ANTHROPIC_API_KEY
  builtinTools:
    - read
    - bash
    - edit
    - write
```

### Trigger a run

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: AgentRun
metadata:
  name: test-run
spec:
  agentRef: my-agent
  source: channel
  sourceRef: manual
  prompt: "List all files in the workspace"
```

### Apply sample resources

```sh
kubectl apply -k config/samples/
```

## Development

### Using the dev pod (recommended)

The dev pod runs in-cluster on k3s with your source code mounted via hostPath. See [`hack/dev/dev-pod.yaml`](hack/dev/dev-pod.yaml) for the full setup.

```sh
# Deploy the dev pod
kubectl apply -f hack/dev/dev-pod.yaml

# Shell in
kubectl exec -it -n agent-system deploy/agentops-dev -- bash

# Inside the pod:
make generate       # regen deepcopy
make manifests      # regen CRD + RBAC manifests
make install        # apply CRDs to cluster
make run            # run operator
```

### Local development

```sh
make generate       # Generate DeepCopy methods
make manifests      # Generate CRD, RBAC, Webhook manifests
go build ./...      # Build
make test           # Run tests (envtest)
make lint           # Run golangci-lint
```

## Images

| Image | Source | Purpose |
|-------|--------|---------|
| `ghcr.io/samyn92/agentops-operator` | `Dockerfile` (repo root) | Kubernetes operator |
| `ghcr.io/samyn92/agent-runtime-fantasy` | [`agentops-runtime-fantasy`](https://github.com/samyn92/agentops-runtime-fantasy) repo | Fantasy SDK agent runtime |
| `ghcr.io/samyn92/mcp-gateway` | `images/mcp-gateway/` | MCP protocol gateway (spawn + proxy modes) |

## Uninstall

```sh
kubectl delete -k config/samples/
make undeploy
make uninstall
```

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
