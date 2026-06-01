# PROPOSAL: GitLab-native Integration (group service-account model)

Status: **Draft v2** — make the `Integration` CRD the single source of truth
for a GitLab identity, consumed consistently by agents, the console, and
(later) event bridges. Standardize on a **GitLab group access token /
service account** scoped over a repo group. **GitLab is a first-class
platform capability and is implemented as a native runtime tool, not a
pluggable OCI/MCP artifact** (decision, see "Native vs pluggable" below).
Target CRD: `integrations.agents.agentops.io`
Related: `agentops-runtime-fantasy` (native `gitlab_*` tools),
`agentops-console` ResourceBrowser, `channels.agents.agentops.io` (events).
Supersedes: `agent-tools/servers/gitlab` (OCI MCP tool — deprecated).

## Native vs pluggable (decision)

The runtime already registers native Go tools via `fantasy.NewAgentTool`
(`bash/read/edit/...`, all git plumbing `git_status/commit/push/...` via
go-git, `mem_*`, `run_agent`, `run_finish`). The GitLab/GitHub **forge API**
was the *only* first-class capability still shipped as an external OCI/MCP
artifact (`agent-tools/servers/gitlab`). That split — native `git push` but
MCP `gitlab_create_mr` — is arbitrary.

**Decision: forges are native; MCP-OCI is kept only for genuine extensions.**

| | Native built-in (in runtime) | MCP-OCI (pluggable) |
|---|------------------------------|---------------------|
| What | Platform first-class: git, **gitlab**, github, memory, delegation | Third-party / customer tools: `kubectl`, `helm`, `flux`, bespoke |
| Why | We own + ship + pin it per runtime release | Add a tool without rebuilding the runtime |

Why this is *not* a security regression: MCP stdio is **not** a sandbox —
the subprocess shares the pod, network, filesystem, and token env with the
runtime. Native is if anything marginally safer (token in one process, not
two). The real risk of a forge agent is **prompt injection + agency** (acting
on a malicious MR body), which is identical native-vs-MCP and is mitigated by
`readOnly`-by-default bindings, a least-privilege group token, and the
project allow-list — see Security.

Module note: the runtime is deliberately standalone (does not import
`agentops-core`). The GitLab client is therefore a **self-contained
`pkg/gitlab`** inside the runtime (no core/Fantasy deps), promotable to a
shared module later. The console keeps its existing thin `/api/v4` browse
proxy (a different consumer shape); a single shared client across both
modules is deferred until a third consumer justifies the coupling.

## Motivation

Today GitLab support grew bottom-up: the `gitlab` MCP tool was built first,
then the `Integration` CRD was added as a descriptor. The result is that the
CRD is **inert metadata** and the real coupling lives in two adapters that
are wired inconsistently:

- **Operator (agentops-core)** never calls GitLab. It only resolves an
  Integration into config/credentials and hands them to a pod. There is no
  GitLab client in the operator and there should not be one — the adapter
  seam is correct.
- **The `gitlab` MCP tool** (`agent-tools/servers/gitlab/main.go`) is the
  agent-side adapter. It reads `GITLAB_TOKEN` / `GITLAB_URL` and calls
  `/api/v4` with a `PRIVATE-TOKEN` header. 11 tools today: project/MR/issue
  read + `create_mr`/`update_mr`/`add_*_note` writes + `get_pipeline`.
- **The console BFF** (`agentops-console internal/handlers/agentresources.go`)
  is the human-side adapter. It already reads the Integration's credential
  Secret (`k8s/client.go GetIntegrationCredentials`) and proxies `/api/v4`
  with `PRIVATE-TOKEN`. Files/commits/branches/MRs/issues browsing already
  works end-to-end.

The gap is **inconsistent consumption**:

| Consumer | Reads Integration config | Gets the credential | Status |
|----------|--------------------------|---------------------|--------|
| Console BFF | yes | yes (reads Secret) | **works** |
| AgentRun git job | yes (`spec.git` ref) | yes (env `GITLAB_TOKEN`) | works, per-run only |
| Daemon agent (`spec.integrations`) | yes (config only) | **no token injected** | broken for API use |
| `gitlab-group` kind | declared, never consumed | n/a | **dead config** |
| `Integration.spec.triggers` | declared, never consumed | n/a | **dead config** (superseded by Channel) |

The vision is a **GitLab service account operating across a large repo
group** to support DevOps engineers in CI/CD and GitOps. That requires:
(1) one group-scoped identity, (2) automatic credential + tool wiring when an
agent binds the Integration, (3) the console browsing issues/MRs/pipelines
across the group — all keyed off **one** Integration CR.

## Principle: Integration is the identity, adapters do the I/O

```
                    ┌──────────────────────────────┐
                    │  Integration (gitlab-group)   │  one CR = one GitLab identity
                    │  baseURL + group + projects[] │
                    │  credentials -> Secret (token)│
                    └───────────────┬───────────────┘
        ┌───────────────────────────┼───────────────────────────┐
        ▼                           ▼                            ▼
  Agent pod (MCP)             Console BFF                  Channel bridge
  gitlab tool +               proxy /api/v4                webhook -> AgentRun
  GITLAB_TOKEN/URL/GROUP      (browse, DONE +pipelines)    (events, future)
  <- P1 GAP                                                <- out of scope here
```

The operator stays **stateless w.r.t. GitLab**. "Native" means the operator
resolves one identity into every consumer consistently — not that the
control plane talks to GitLab. Swapping gitlab.com ↔ self-hosted is a
`baseURL` change; swapping the GitLab adapter is replacing the MCP tool.

## Identity model: group access token / service account

Standardize on **one GitLab group access token** (or a service-account PAT
with group membership) per `gitlab-group` Integration. That token *is* the
service-account identity over the repo group.

- `gitlab-project` Integrations may still carry their own project token for
  isolated cases, but the primary model is group-scoped.
- `GitLabGroupResourceConfig.Projects []string` becomes the **allow-list**
  the operator passes to the agent/tool to scope which repos under the group
  the agent may act on (defense in depth on top of the token's own scope).

## Design

### CRD: make `gitlab-group` first-class (already in schema)

No new fields required for P1/P2 — `GitLabGroupResourceConfig` already has
`BaseURL`, `Group`, `Projects []string`, and `Integration.spec.credentials`.
The change is **consuming** them. Optional later additions:

```go
// GitLabGroupResourceConfig (additions, future)
type GitLabGroupResourceConfig struct {
    BaseURL  string   `json:"baseURL"`
    Group    string   `json:"group"`
    Projects []string `json:"projects,omitempty"` // allow-list (now consumed)

    // ReadOnly forces the group identity read-only regardless of token
    // scope; the operator sets GITLAB_READONLY=true for bound agents.
    // +optional
    ReadOnly bool `json:"readOnly,omitempty"`
}
```

### Operator: wire the bound Integration into the agent pod (P1)

When an Agent's `spec.integrations[]` binds a `gitlab-project`/`gitlab-group`
Integration, the operator injects the same env contract the MCP tool already
expects, into the **daemon agent pod** (extend the ConfigMap/pod path at
`internal/resources/configmap.go`, reusing the env-injection that the
AgentRun git path already does at `internal/resources/agent_git.go:155`):

```
GITLAB_URL      = <baseURL>
GITLAB_TOKEN    = <from Integration.spec.credentials secretRef>     # never logged
GITLAB_GROUP    = <group>          # gitlab-group only
GITLAB_PROJECT  = <project>        # gitlab-project only
GITLAB_PROJECTS = <comma-joined allow-list>   # gitlab-group, optional
GITLAB_READONLY = true|false       # from IntegrationBinding.ReadOnly or group ReadOnly
```

And auto-register the `gitlab` tool mount when a gitlab Integration is bound
and the tool OCI artifact is available, so operators stop declaring the tool
+ env by hand. Today `IntegrationBinding.ReadOnly` is documented as
"enforced by runtime" but nothing enforces it; `GITLAB_READONLY` makes it
real (see tool change below).

**No operator GitLab client. No new RBAC beyond Secret read the operator
already has for credential resolution.**

### Runtime: native `gitlab_*` tools (`agentops-runtime-fantasy`)

A self-contained `pkg/gitlab` client (`/api/v4`, `PRIVATE-TOKEN` auth,
read-only guard) plus native tools registered beside the git tools in
`gitTools()`/`main.go`, auto-enabled when `GITLAB_TOKEN` is present:

- Read: `gitlab_get_project`, `gitlab_list_mrs`, `gitlab_get_mr`,
  `gitlab_get_mr_diff`, `gitlab_list_issues`, `gitlab_get_issue`.
- Write (gated off when `GITLAB_READONLY=true`): `gitlab_create_mr`,
  `gitlab_update_mr`, `gitlab_add_mr_note`, `gitlab_add_issue_note`.
- Group: `gitlab_list_group_projects` (enumerate under `GITLAB_GROUP`,
  honoring the `GITLAB_PROJECTS` allow-list), `gitlab_search`.
- CI/GitOps: `gitlab_list_pipelines`, `gitlab_get_pipeline`,
  `gitlab_get_job`, `gitlab_get_job_log`, `gitlab_retry_pipeline`,
  `gitlab_trigger_pipeline`.

The OCI tool `agent-tools/servers/gitlab` is **deprecated** — same surface,
now in-runtime. Agents stop declaring it in `spec.tools[]`.

### Console: finish the browse adapter (P3)

The browse panel is already Integration-native (keyed by Integration name,
reads its Secret). Remaining work:

1. **Pipelines tab** — the only missing capability. New
   `BrowseResourcePipelines` handler mirroring `BrowseResourceMergeRequests`
   (`/api/v4/projects/{id}/pipelines`), register alongside the existing
   `/integrations/{name}/{mergerequests,issues}` routes, add
   `agentResources.pipelines()` in `web/src/lib/api.ts`, a `'pipelines'` tab
   in `web/src/components/resources/ResourceBrowser.tsx`, and a `GitPipeline`
   type in `web/src/types/api.ts`.
2. **Fix the live route mismatch** — backend routes moved to
   `/integrations/{name}/...` (`server.go:119-124`) but the frontend
   `api.ts` still calls `/agents/{ns}/{name}/resources/...`. The browse panel
   may be broken today; verify on the cluster and align paths.
3. **Group drill-down** — when the Integration is `gitlab-group`, list group
   projects, then drill into MRs/issues/pipelines per project. Pairs with the
   group identity model.

### Triggers: deprecate, do not extend

`Integration.spec.triggers` is dead config superseded by the `Channel` CRD
(deployed bridge + webhook + SA token → AgentRun). Do **not** build group
event ingestion on Integration triggers. If group-wide event handling is
wanted later, extend `Channel` (group webhook fan-out) and mark
`Integration.spec.triggers` deprecated in v1alpha1, remove in v1beta1.

## Security

- Group token is a **single high-value credential**. Store in a Secret,
  never logged, never returned by the BFF (the BFF returns API *results*, not
  the token; `ListSecretsMetadata` already returns key names only).
- Prefer a **GitLab group access token** with least-privilege role
  (Reporter for read agents, Developer for MR-creating agents) over a
  personal PAT, so it's tied to the group, rotatable, and not a human.
- `GITLAB_READONLY` + `Projects` allow-list give two independent guards on
  top of the token's own scope.
- One Integration per trust boundary: a read-only browse identity and a
  write-capable GitOps identity can be two separate `gitlab-group`
  Integrations over the same group with different tokens.

## Rollout plan

1. **PR 1 — agentops-core (P1, highest leverage)**
   - Operator injects `GITLAB_*` env into daemon agents from a bound gitlab
     Integration (extend `internal/resources/configmap.go`, reuse
     `agent_git.go` env helpers).
   - Auto-register the `gitlab` tool mount when bound + available.
   - Plumb `IntegrationBinding.ReadOnly` → `GITLAB_READONLY`.
   - `op-reload` (controller/resources only; no CRD type change needed).

2. **PR 2 — agent-tools/servers/gitlab (P2 group SA)**
   - `gitlab_list_group_projects`, `gitlab_search`, allow-list enforcement.
   - `GITLAB_READONLY` gating of write tools.
   - Pipeline/job tools for CI/GitOps.
   - Re-package + push OCI tool artifact; bump tool version.

3. **PR 3 — agentops-console (P3)**
   - Pipelines tab end-to-end + route-mismatch fix.
   - Group drill-down for `gitlab-group` Integrations.

4. **Phase 2 (separate plan) — Channel group event ingestion**
   - Group webhook → fan-out to per-project AgentRuns.
   - Deprecate `Integration.spec.triggers`.

## Open Questions

- **Q1** Token type: standardize on **GitLab group access token** (resolved:
  group service-account model). Personal PAT only as a stopgap.
- **Q2** Should the operator auto-register the `gitlab` tool, or require the
  agent to still list it explicitly (auto is more "native", explicit is more
  predictable)? Leaning auto with an opt-out.
- **Q3** Allow-list enforcement location: client-side in the tool (proposed)
  vs. also validated by the operator at bind time. Tool-side is sufficient;
  operator-side is defense in depth.
- **Q4** Do daemon agents need write access by default, or should
  `readOnly: true` be the binding default for group identities? Leaning
  read-only-by-default, opt-in to write.
