# agentops-core

## Development Environment

We develop on a local k3s cluster (single node, `pc-omarchy`). The dev pod runs in-cluster with source code mounted via hostPath.

Dev setup lives in the workspace-level `clusters/local_k3s/deploy/` directory. See the root `AGENTS.md` for the full layout.

### Dev Workflow

Use the justfile recipes — all commands exec into the `operator-dev` pod:

```sh
just --justfile clusters/local_k3s/deploy/justfile <recipe>
```

| Recipe | When to use |
|--------|-------------|
| `just op-reload` | Changed controller logic (`internal/controller/`, `internal/resources/`, `cmd/`). Kills manager, rebuilds, restarts. ~3-5s. |
| `just op-reload-full` | Changed CRD types (`api/v1alpha1/*_types.go`). Runs `make generate` + `make manifests` + `make install` + restart. |
| `just op-build` | Compile check only, no restart. |
| `just op-logs` | Tail operator logs (follow). |
| `just op-stop` | Stop the operator process. |
| `just op-shell` | Interactive shell into the pod. |

### When to use which reload

- **`op-reload`** (fast path) — any change under `internal/` or `cmd/`. Skips CRD generation entirely.
- **`op-reload-full`** (full path) — when you modify struct fields, markers, or annotations in `api/v1alpha1/*_types.go` or `api/v1alpha1/shared_types.go`. This regenerates DeepCopy methods, CRD YAML, and reinstalls CRDs into the cluster.

### Kubernetes Context

We test on the `k3s` context (not `homecluster`):

```sh
kubectl config use-context k3s
```

### CRDs

5 CRDs in API group `agents.agentops.io/v1alpha1`:

- `agents.agents.agentops.io` (short: `ag`)
- `agentruns.agents.agentops.io` (short: `ar`)
- `channels.agents.agentops.io` (short: `ch`)
- `agenttools.agents.agentops.io`
- `agentresources.agents.agentops.io`

### Namespaces

| Namespace | Purpose |
|-----------|---------|
| `agent-system` | Dev pods, operator, console |
| `agents` | Agent workloads, Engram |

### Runtime

The operator uses the **Charm Fantasy SDK (Go)** as its sole agent runtime.
The runtime is developed in the standalone repo `agentops-runtime`.

### Images

| Image | Source | Purpose |
|-------|--------|---------|
| `ghcr.io/samyn92/agentops-operator` | `Dockerfile` (repo root) | Kubernetes operator |
| `ghcr.io/samyn92/agentops-runtime-fantasy` | `agentops-runtime` repo | Fantasy SDK agent runtime |
| `ghcr.io/samyn92/mcp-gateway` | `images/mcp-gateway/` | MCP protocol gateway (spawn + proxy modes) |

### Related Repos

| Repo | Purpose |
|------|---------|
| `agentops-runtime` | Fantasy agent runtime (Go, Charm Fantasy SDK) |
| `agentops-console` | Web console (Go BFF + SolidJS PWA) |
| `agent-channels` | Channel bridge images (gitlab, webhook, etc.) |
| `agent-tools` | OCI tool/agent packaging CLI + tool packages |
| `agent-factory` | Helm chart for deploying agents |
