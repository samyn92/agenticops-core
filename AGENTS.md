# agentops-core

## Development Environment

We develop on a local k3s cluster (single node, `pc-omarchy`). The dev pod runs in-cluster with your source code mounted via hostPath.

### Dev Pod Setup

Deploy the dev pod (namespace: `agent-system`):

```sh
kubectl apply -f hack/dev/dev-pod.yaml
```

This creates:
- ServiceAccount + ClusterRole with full operator permissions
- Deployment running `golang:1.26` with an init script that installs kubectl, vim, jq, git
- hostPath mount of this repo into `/workspace`

### Workflow

Edit code locally on your machine (files are live via hostPath), then run in the dev pod:

```sh
# Build check (from local or dev pod)
kubectl exec -n agent-system deploy/agentops-dev -- bash -c \
  "cd /workspace && go build ./..."

# Apply changes to the cluster (dev pod, PATH required)
kubectl exec -n agent-system deploy/agentops-dev -- bash -c \
  "cd /workspace && export PATH=/usr/local/bin:/go/bin:/usr/local/go/bin:\$PATH && \
   make generate && make manifests && make install"

# Restart the operator (kill old process first if running)
kubectl exec -n agent-system deploy/agentops-dev -- bash -c \
  "cd /workspace && export PATH=/usr/local/bin:/go/bin:/usr/local/go/bin:\$PATH && \
   pkill -f manager; go build -o /tmp/manager ./cmd/main.go && \
   nohup /tmp/manager > /tmp/operator.log 2>&1 & echo \$!"

# Verify operator is running
kubectl exec -n agent-system deploy/agentops-dev -- bash -c \
  "pgrep -f manager && tail -5 /tmp/operator.log"
```

**Important:** `kubectl` is at `/usr/local/bin/kubectl` inside the dev pod but `make` subshells don't always inherit PATH — always export it explicitly.

### Kubernetes Context

We test on the `k3s` context (not `homecluster`):

```sh
kubectl config use-context k3s
```

### CRDs

4 CRDs in API group `agents.agentops.io/v1alpha1`:

- `agents.agents.agentops.io` (short: `ag`)
- `channels.agents.agentops.io` (short: `ch`)
- `agentruns.agents.agentops.io` (short: `ar`)
- `mcpservers.agents.agentops.io` (short: `mcp`)

### Namespaces

| Namespace | Purpose |
|-----------|---------|
| `agent-system` | Dev pod, operator, console |
| `agents` | Agent workloads (created when deploying CRs) |

### Runtime

The operator uses the **Charm Fantasy SDK (Go)** as its sole agent runtime.
The runtime is developed in the standalone repo `agentops-runtime-fantasy`.

### Images

| Image | Source | Purpose |
|-------|--------|---------|
| `ghcr.io/samyn92/agentops-operator` | `Dockerfile` (repo root) | Kubernetes operator |
| `ghcr.io/samyn92/agent-runtime-fantasy` | `agentops-runtime-fantasy` repo | Fantasy SDK agent runtime |
| `ghcr.io/samyn92/mcp-gateway` | `images/mcp-gateway/` | MCP protocol gateway (spawn + proxy modes) |

### Related Repos

| Repo | Purpose |
|------|---------|
| `agentops-runtime-fantasy` | Fantasy agent runtime (Go, Charm Fantasy SDK) |
| `agent-channels` | Channel bridge images (gitlab, webhook, etc.) |
| `agent-tools` | OCI tool/agent packaging CLI + tool packages |
| `agent-console` | Web console |
| `agent-factory` | Helm chart (future) |
