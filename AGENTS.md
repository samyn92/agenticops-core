# agenticops-core

## Development Environment

We develop on a local k3s cluster (single node, `pc-omarchy`). The dev pod runs in-cluster with your source code mounted via hostPath.

### Dev Pod Setup

Deploy the dev pod (namespace: `agent-system`):

```sh
kubectl apply -f hack/dev/dev-pod.yaml
```

This creates:
- ServiceAccount + ClusterRole with full operator permissions
- Deployment running `golang:1.25` with an init script that installs Node 22, kubectl, vim, jq, git
- hostPath mount of this repo into `/workspace`

### Workflow

Edit code locally on your machine (files are live via hostPath), then shell into the dev pod:

```sh
kubectl exec -it -n agent-system deploy/agenticops-dev -- bash
```

Inside the pod:

```sh
make generate && make manifests    # regen deepcopy + CRD manifests
make install                       # apply CRDs to k3s
make run                           # run operator against k3s
go build ./...                     # build check
```

### Kubernetes Context

We test on the `k3s` context (not `homecluster`):

```sh
kubectl config use-context k3s
```

### CRDs

4 CRDs in API group `agents.agenticops.io/v1alpha1`:

- `agents.agents.agenticops.io` (short: `ag`)
- `channels.agents.agenticops.io` (short: `ch`)
- `agentruns.agents.agenticops.io` (short: `ar`)
- `mcpservers.agents.agenticops.io` (short: `mcp`)

### Namespaces

| Namespace | Purpose |
|-----------|---------|
| `agent-system` | Dev pod, operator, console |
| `agents` | Agent workloads (created when deploying CRs) |
