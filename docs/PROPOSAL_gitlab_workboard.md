# GitLab Work Board — Label Protocol & Contract (Draft v1)

> Companion to `PROPOSAL_gitlab_native_integration.md`.
> This is the **authoritative cross-component contract** for the GitLab-native
> work board: the label state machine, the poll trigger, the agent↔card join,
> and the human merge gate. Operator (agentops-core), bridge (agent-channels),
> runtime (agentops-runtime), and console all conform to this document.

---

## 1. Vision in one paragraph

The primary surface becomes a **board of work items** (GitLab issues/MRs) that
agents execute, with chat/traces as drill-down. GitLab is the **work graph**, the
Channel CRD is the **trigger**, the Integration CRD is the agent's **identity +
workspace**, the native `gitlab_*` runtime tools are the **action surface**, and
**labels are the state machine**. We do **not** build a task queue or rebuild
GitLab boards — we overlay agent runs/traces on real GitLab data, and the Merge
Request is the **hard human barrier** between agent intent and infra change.

---

## 2. Label state machine

A single scoped label set drives column placement. Labels are **mutually
exclusive** within the `agent::` scope (GitLab scoped labels with `::` enforce
single-value-per-scope automatically).

| Label | Column | Set by | Meaning |
|-------|--------|--------|---------|
| `agent::todo` | Todo | human (or auto-triage) | Ready for an agent to pick up |
| `agent::in-progress` | In Progress | **agent** (first action) | Agent is actively working |
| `agent::needs-review` | Needs Review | **agent** (on MR open) | Work done, MR open, awaiting human |
| `agent::changes-requested` | Changes Requested | human (review) | Human wants changes; agent re-runs |
| `agent::approved` | Done | human | Approved; merged → terminal |

### Transitions

```
                 ┌─────────────── human creates issue + agent::todo
                 ▼
          ┌─────────────┐   agent picks up (poll match)
          │ agent::todo │──────────────┐
          └─────────────┘              ▼
                              ┌────────────────────┐
                              │ agent::in-progress │  agent sets this,
                              └────────────────────┘  removing agent::todo
                                         │ agent opens MR
                                         ▼
                              ┌──────────────────────┐
              ┌──────────────►│ agent::needs-review  │
              │               └──────────────────────┘
              │                  │                 │
  human: request changes         │ human approves  │
              │                  ▼                 ▼
   ┌──────────────────────────┐  merge MR    ┌───────────────┐
   │ agent::changes-requested │  (human gate)│ agent::approved│→ MR merged → Done
   └──────────────────────────┘              └───────────────┘
              │ agent re-runs (poll match), back to in-progress
              └──────────────────────────────►
```

**Idempotency is a property of the protocol, not bookkeeping.** The poll trigger
matches `agent::todo` (and, for re-runs, `agent::changes-requested`). The agent's
**first tool call** sets `agent::in-progress`, which removes the matched label, so
the item stops matching and will not re-fire. The bridge keeps a short in-memory
seen-set only to cover the brief transition window before the label flips.

### Who is allowed to do what

- **Agents** may set `agent::in-progress` and `agent::needs-review` (via the
  `gitlab_update_issue` runtime tool) and open/update MRs and post notes.
- **Agents may NOT merge.** There is deliberately **no agent merge tool**. Merging
  to a protected branch is human-only — this is the gate. Flux reconciles infra
  only after a human merges.
- **Humans** set `agent::changes-requested` / `agent::approved` and perform the
  merge (from the console Board or GitLab directly).

---

## 3. Trigger contract (Channel `type: gitlab-label`, poll-based)

The operator renders a **bridge Deployment** (image
`ghcr.io/samyn92/agent-channel-gitlab-label`). The operator stays pure: it injects
the bound Integration's token via a `SecretKeyRef` env (exactly like
`WEBHOOK_SECRET`), and the bridge runs the poll loop.

### ChannelSpec (new sibling config)

```yaml
apiVersion: agents.agentops.io/v1alpha1
kind: Channel
metadata:
  name: homecluster-board
  namespace: agents
spec:
  type: gitlab-label
  agentRef: homecluster-manager
  image: ghcr.io/samyn92/agent-channel-gitlab-label:<version>
  prompt: |
    A GitLab {{ .gitlab.target }} needs attention.
    Title: {{ .item.title }}
    URL:   {{ .item.web_url }}
    #{{ .item.iid }}

    First, set the label agent::in-progress (removing agent::todo) using
    gitlab_update_issue. Then complete the work, open a merge request, and set
    the label agent::needs-review.
  gitlabLabel:
    integrationRef: homecluster-repo     # bound gitlab-project / gitlab-group Integration
    target: issues                        # issues | merge_requests
    labels: ["agent::todo", "agent::changes-requested"]
    state: opened                         # opened | all
    pollInterval: 30s
```

### Env contract (operator → bridge)

Existing (reused): `CHANNEL_TYPE`, `AGENT_REF`, `AGENT_URL`, `AGENT_MODE`,
`CHANNEL_NAME`, `PROMPT_TEMPLATE`, `POD_NAMESPACE`.

New (operator injects from the bound Integration + channel config):

| Env | Source |
|-----|--------|
| `GITLAB_BASE_URL` | `Integration.spec.gitlab.baseURL` (or group) |
| `GITLAB_PROJECT` / `GITLAB_GROUP` | Integration project/group |
| `GITLAB_TOKEN` | `SecretKeyRef` → `Integration.spec.credentials` (never read by operator) |
| `GITLAB_TARGET` | `issues` \| `merge_requests` |
| `GITLAB_LABELS` | CSV of trigger labels |
| `GITLAB_STATE` | `opened` \| `all` |
| `GITLAB_POLL_INTERVAL` | duration string (e.g. `30s`) |

### Poll loop (bridge, stdlib `net/http`, zero-dep)

1. Every `GITLAB_POLL_INTERVAL`, `GET /api/v4/projects/{id}/{target}?labels=<csv>&state=<state>&updated_after=<watermark>&order_by=updated_at` with header `PRIVATE-TOKEN: $GITLAB_TOKEN`.
2. For each item whose `iid` is not in the seen-set: render the prompt template, fire the agent (daemon → `POST /prompt`; task → create `AgentRun`), add `iid` to the seen-set, advance the `updated_after` watermark.
3. Health: still serves `/healthz` on `:8080` for k8s probes; graceful shutdown on SIGTERM.

---

## 4. Agent ↔ card join (the overlay)

When the bridge fires, it **stamps the AgentRun** so the console Board can join a
run to its card without guessing:

```yaml
metadata:
  annotations:
    agentops.dev/gitlab-iid: "139"
    agentops.dev/gitlab-target: "issues"        # issues | merge_requests
    agentops.dev/gitlab-project: "samyn92/homecluster"
  labels:
    agents.agentops.io/channel: homecluster-board
    agents.agentops.io/source: channel
```

The console lists `AgentRun`s, indexes them by `(project, target, iid)`, and
overlays the live run banner (status, current step, `status.traceID` → trace) onto
the matching board card. The drawer's **Live Trace** tab is driven by
`run.status.traceID`; **Overview** reuses `RunDetailView`; **Diff & Gate** reuses
`DiffCard` over the MR `/changes` plus the human merge action.

---

## 5. Human merge gate (console write actions)

The Board issues these **human-initiated** calls through the console BFF
(write-capable GitLab proxy, distinct from agent tools):

| Action | GitLab call |
|--------|-------------|
| Post review note | `POST /projects/{id}/merge_requests/{iid}/notes` |
| Request changes | `PUT /projects/{id}/issues/{iid}` (set `agent::changes-requested`) |
| Approve | `PUT /projects/{id}/issues/{iid}` (set `agent::approved`) |
| Merge | `PUT /projects/{id}/merge_requests/{iid}/merge` |

These are gated to human users; agents never receive a merge tool.

---

## 6. Non-goals (explicit)

- **No task queue.** GitLab assignee/label is the lock and the state; we don't
  build a separate queue.
- **No rebuilt GitLab boards.** We overlay agent runs/traces on real GitLab data.
- **No webhooks (yet).** Poll-now, webhook-ready-later: the poller is the source of
  truth; a future webhook would only be a low-latency hint that triggers an
  immediate poll. Works behind NAT today.
- **No agent merges.** Human gate is non-negotiable.

---

## 7. Component checklist

| Component | Change |
|-----------|--------|
| agentops-runtime | add `gitlab_update_issue` native tool (labels/assignee, readOnly-gated) |
| agentops-core | `Channel.type=gitlab-label` + `GitLabLabelChannelConfig`; bridge Deployment render + env injection; CEL validation |
| agent-channels | new `channels/gitlab-label` poll loop; stamp AgentRun annotations; Dockerfile + CI |
| agentops-console | write-capable GitLab proxy + Board view + drill-down drawer + merge gate |
