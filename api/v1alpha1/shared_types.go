/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

// -------------------------------------------------------------------
// Secret references
// -------------------------------------------------------------------

// SecretKeyRef references a key in a Kubernetes Secret (same namespace).
type SecretKeyRef struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key within the Secret.
	Key string `json:"key"`
}

// SecretEnvVar injects a Secret key as an environment variable.
type SecretEnvVar struct {
	// Environment variable name.
	Name string `json:"name"`
	// Reference to the secret key.
	SecretRef SecretKeyRef `json:"secretRef"`
}

// -------------------------------------------------------------------
// OCI artifacts
// -------------------------------------------------------------------

// OCIRef references an OCI artifact.
type OCIRef struct {
	// Full OCI reference (e.g. ghcr.io/org/tool:1.0.0).
	Ref string `json:"ref"`
	// Optional digest for pinning (e.g. sha256:abc...).
	// +optional
	Digest string `json:"digest,omitempty"`
	// Pull policy: Always, IfNotPresent, Never. Defaults based on tag.
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	PullPolicy string `json:"pullPolicy,omitempty"`
	// Optional pull secret for private registries.
	// +optional
	PullSecret *SecretKeyRef `json:"pullSecret,omitempty"`
}

// -------------------------------------------------------------------
// Provider binding
// -------------------------------------------------------------------

// ProviderBinding references a Provider CR with optional per-agent overrides.
type ProviderBinding struct {
	// Name of the Provider CR in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Override per-call defaults from the Provider CR for this agent.
	// +optional
	Overrides *ProviderCallDefaults `json:"overrides,omitempty"`
}

// -------------------------------------------------------------------
// Context files
// -------------------------------------------------------------------

// ContextFileRef loads an AGENTS.md or similar context file from a ConfigMap.
type ContextFileRef struct {
	// ConfigMap reference (name + key).
	ConfigMapRef SecretKeyRef `json:"configMapRef"`
}

// -------------------------------------------------------------------
// AgentTool bindings (per-agent)
// -------------------------------------------------------------------

// AgentToolBinding references an AgentTool CR with optional per-agent overrides.
type AgentToolBinding struct {
	// Name of the AgentTool CR.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Override permissions from AgentTool defaults.
	// +optional
	Permissions *MCPPermissions `json:"permissions,omitempty"`
	// MCP tools to promote to first-class (mcpServer/mcpEndpoint sources only).
	// +optional
	DirectTools []string `json:"directTools,omitempty"`
	// Auto-inject skill content into every prompt (skill sources only).
	// +optional
	AutoContext bool `json:"autoContext,omitempty"`
}

// -------------------------------------------------------------------
// MCP permissions (used by AgentToolBinding)
// -------------------------------------------------------------------

// MCPPermissions configures deny/allow rules for MCP tool calls on the proxy gateway.
type MCPPermissions struct {
	// Mode: "deny" blocks matching rules, "allow" only permits matching rules.
	// +kubebuilder:validation:Enum=deny;allow
	Mode string `json:"mode"`
	// Rules in the form "tool_name", "tool_name:arg=value", or "tool_name:arg=pattern*".
	Rules []string `json:"rules"`
}

// -------------------------------------------------------------------
// Tool hooks (defense-in-depth + memory integration)
// -------------------------------------------------------------------

// ToolHooksSpec configures defense-in-depth runtime constraints on tool calls,
// and declarative memory integration (audit persistence, auto-capture, context injection).
type ToolHooksSpec struct {
	// Patterns blocked in bash commands (substring match).
	// +optional
	BlockedCommands []string `json:"blockedCommands,omitempty"`
	// Restrict file tool paths to these prefixes (absolute paths only).
	// +optional
	AllowedPaths []string `json:"allowedPaths,omitempty"`
	// Tools to audit-log via afterToolCall hook. When memory is enabled,
	// audit events are also persisted as searchable observations.
	// +optional
	AuditTools []string `json:"auditTools,omitempty"`
	// Declarative rules for auto-saving tool results as memory observations.
	// Each rule matches a tool name and optionally filters by output/args regex.
	// +optional
	MemorySaveRules []MemorySaveRuleSpec `json:"memorySaveRules,omitempty"`
	// Pre-execution memory queries: before a matched tool runs, relevant
	// memories are fetched and recorded in the trace for observability.
	// +optional
	ContextInjectTools []ContextInjectRuleSpec `json:"contextInjectTools,omitempty"`
}

// MemorySaveRuleSpec describes a declarative rule for auto-saving tool results
// as memory observations in agentops-memory.
type MemorySaveRuleSpec struct {
	// Tool name to match (e.g. "bash", "web_search").
	Tool string `json:"tool"`
	// Regex pattern to match against tool output. If empty, all output is captured.
	// +optional
	MatchOutput string `json:"matchOutput,omitempty"`
	// Map of arg_name → regex pattern. All patterns must match for the rule to fire.
	// +optional
	MatchArgs map[string]string `json:"matchArgs,omitempty"`
	// Observation type to save (e.g. "bugfix", "discovery"). Default: "discovery".
	// +optional
	// +kubebuilder:default=discovery
	Type string `json:"type,omitempty"`
	// Scope for the observation. Default: "project".
	// +optional
	// +kubebuilder:default=project
	// +kubebuilder:validation:Enum=project;global
	Scope string `json:"scope,omitempty"`
}

// ContextInjectRuleSpec describes a pre-execution memory query for a tool.
type ContextInjectRuleSpec struct {
	// Tool name to match.
	Tool string `json:"tool"`
	// How to derive the search query. "from_tool_args" (default) uses the tool's
	// primary argument. Any other value is used as a static query string.
	// +optional
	// +kubebuilder:default=from_tool_args
	Query string `json:"query,omitempty"`
	// Max number of memory items to fetch. Default: 3.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	Limit int `json:"limit,omitempty"`
}

// -------------------------------------------------------------------
// Concurrency
// -------------------------------------------------------------------

// ConcurrencySpec controls parallel AgentRun execution.
type ConcurrencySpec struct {
	// Maximum concurrent runs. Default: 1.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	MaxRuns int `json:"maxRuns,omitempty"`
	// Policy when at max: queue, reject, or replace.
	// +optional
	// +kubebuilder:default=queue
	// +kubebuilder:validation:Enum=queue;reject;replace
	Policy string `json:"policy,omitempty"`
}

// -------------------------------------------------------------------
// Storage (daemon agents)
// -------------------------------------------------------------------

// StorageSpec defines PVC configuration for daemon agents.
type StorageSpec struct {
	// PVC size (e.g. "10Gi").
	Size string `json:"size"`
	// Storage class name.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`
}

// -------------------------------------------------------------------
// Network policy
// -------------------------------------------------------------------

// NetworkPolicySpec controls agent network isolation.
type NetworkPolicySpec struct {
	// Whether to create a NetworkPolicy.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// -------------------------------------------------------------------
// Channel platform configs
// -------------------------------------------------------------------

// TelegramChannelConfig configures a Telegram bot channel.
type TelegramChannelConfig struct {
	// Secret containing the bot token.
	BotTokenSecret SecretKeyRef `json:"botTokenSecret"`
	// Allowed Telegram user IDs.
	// +optional
	AllowedUsers []string `json:"allowedUsers,omitempty"`
	// Allowed Telegram chat IDs.
	// +optional
	AllowedChats []string `json:"allowedChats,omitempty"`
}

// SlackChannelConfig configures a Slack channel.
type SlackChannelConfig struct {
	// Secret containing the bot token.
	BotTokenSecret SecretKeyRef `json:"botTokenSecret"`
	// Allowed Slack channel IDs.
	// +optional
	AllowedChannels []string `json:"allowedChannels,omitempty"`
}

// DiscordChannelConfig configures a Discord channel.
type DiscordChannelConfig struct {
	// Secret containing the bot token.
	BotTokenSecret SecretKeyRef `json:"botTokenSecret"`
	// Allowed Discord channel IDs.
	// +optional
	AllowedChannels []string `json:"allowedChannels,omitempty"`
}

// GitLabChannelConfig configures a GitLab webhook channel.
type GitLabChannelConfig struct {
	// GitLab webhook events to listen for (e.g. "Issue Hook").
	Events []string `json:"events"`
	// Filter by action (e.g. "open").
	// +optional
	Actions []string `json:"actions,omitempty"`
	// Filter by labels on the object.
	// +optional
	Labels []string `json:"labels,omitempty"`
	// Webhook secret for signature verification.
	Secret SecretKeyRef `json:"secret"`
}

// GitHubChannelConfig configures a GitHub webhook channel.
type GitHubChannelConfig struct {
	// GitHub webhook events to listen for (e.g. "pull_request").
	Events []string `json:"events"`
	// Filter by action (e.g. "opened", "synchronize").
	// +optional
	Actions []string `json:"actions,omitempty"`
	// Filter by labels on the object.
	// +optional
	Labels []string `json:"labels,omitempty"`
	// Webhook secret for signature verification.
	Secret SecretKeyRef `json:"secret"`
}

// WebhookChannelConfig configures a generic webhook channel.
type WebhookChannelConfig struct {
	// Optional HMAC secret for signature verification.
	// +optional
	Secret *SecretKeyRef `json:"secret,omitempty"`
}

// WebhookIngressConfig configures ingress for webhook-based channels.
type WebhookIngressConfig struct {
	// Hostname for the ingress.
	Host string `json:"host"`
	// Path (defaults to /).
	// +optional
	Path string `json:"path,omitempty"`
	// Ingress class name.
	// +optional
	IngressClassName string `json:"ingressClassName,omitempty"`
	// TLS configuration.
	// +optional
	TLS *IngressTLS `json:"tls,omitempty"`
}

// IngressTLS configures TLS for webhook ingress.
type IngressTLS struct {
	// Cert-manager cluster issuer name.
	ClusterIssuer string `json:"clusterIssuer"`
}

// -------------------------------------------------------------------
// Agent resource bindings
// -------------------------------------------------------------------

// AgentResourceBinding references an AgentResource CR from an Agent.
type AgentResourceBinding struct {
	// Name of the AgentResource CR to bind.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Mark the resource as read-only for the agent (advisory, enforced by runtime).
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`

	// Automatically inject this resource context into every prompt
	// without requiring manual selection in the UI.
	// +optional
	AutoContext bool `json:"autoContext,omitempty"`
}

// -------------------------------------------------------------------
// Delegation
// -------------------------------------------------------------------

// DelegationSpec controls how this agent delegates work to other agents.
// The team list IS the access control — only agents listed in Team can be
// delegated to. Process/workflow belongs in the agent's systemPrompt, not
// in CRD fields.
// When set, the operator generates a delegation protocol section that is
// injected into the system message as a separate platform protocol part,
// never appended to the user's systemPrompt.
type DelegationSpec struct {
	// Explicit team roster. Only these agents can be delegated to.
	// Each entry is an Agent CR name in the same namespace.
	// +kubebuilder:validation:MinItems=1
	Team []string `json:"team"`

	// MaxFanOut limits the number of concurrent delegations in a single
	// run_agents call. Runtime-enforced cap on batch size.
	// Default: 5. Hard max: 10 (existing run_agents limit).
	// +optional
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxFanOut int `json:"maxFanOut,omitempty"`
}

// -------------------------------------------------------------------
// Memory (Engram)
// -------------------------------------------------------------------

// MemorySpec configures the Engram shared memory system for an agent.
type MemorySpec struct {
	// Reference to the memory server. Can be an AgentTool CR name
	// (with mcpServer/mcpEndpoint source) or a plain service name.
	// The runtime connects to Engram's REST API via the resolved
	// service URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServerRef string `json:"serverRef"`

	// Project name used to scope memories in Engram.
	// Defaults to the Agent CR name if unset.
	// +optional
	Project string `json:"project,omitempty"`

	// Number of recent context entries injected per turn.
	// +optional
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=50
	ContextLimit int `json:"contextLimit,omitempty"`

	// Enable auto-summarization of sessions at end.
	// +optional
	// +kubebuilder:default=true
	AutoSummarize *bool `json:"autoSummarize,omitempty"`

	// Allow the agent to autonomously save memories via mem_save after
	// completing meaningful work. When false, only the user can create
	// memories through the console UI.
	// +optional
	// +kubebuilder:default=true
	AutoSave *bool `json:"autoSave,omitempty"`

	// Allow the agent to autonomously search memories via mem_search
	// when the user references past work. When false, the agent never
	// searches memory on its own — the user browses/searches via the
	// console Memory panel.
	// +optional
	// +kubebuilder:default=true
	AutoSearch *bool `json:"autoSearch,omitempty"`
}

// -------------------------------------------------------------------
// MCP health check (used by AgentTool mcpServer/mcpEndpoint sources)
// -------------------------------------------------------------------

// MCPHealthCheck configures health probing for MCP tools.
type MCPHealthCheck struct {
	// Health check path (deploy mode) or full URL (external mode).
	// +optional
	Path string `json:"path,omitempty"`
	// Health check URL (external mode only).
	// +optional
	URL string `json:"url,omitempty"`
	// Probe interval in seconds.
	// +optional
	// +kubebuilder:default=30
	IntervalSeconds int `json:"intervalSeconds,omitempty"`
}

// -------------------------------------------------------------------
// MCP OAuth (used by AgentTool mcpEndpoint source)
// -------------------------------------------------------------------

// MCPOAuthConfig configures OAuth for external MCP servers.
type MCPOAuthConfig struct {
	// Secret containing the OAuth client ID.
	ClientIDSecret SecretKeyRef `json:"clientIdSecret"`
	// Secret containing the OAuth client secret.
	ClientSecretSecret SecretKeyRef `json:"clientSecretSecret"`
}

// -------------------------------------------------------------------
// Security overrides
// -------------------------------------------------------------------

// SecurityOverrides exposes a narrow, safety-floor-respecting subset of
// pod- and container-level SecurityContext fields. The operator merges
// these on top of restricted-by-default values; any override that would
// weaken the restricted Pod Security Standard (e.g. RunAsNonRoot=false,
// AllowPrivilegeEscalation=true, ReadOnlyRootFilesystem=false, root UID,
// adding capabilities, Unconfined seccomp) is silently clamped and the
// owning resource gets a SecurityPolicyViolations condition listing every
// rejected field.
//
// All fields are optional. Defaults are documented on each helper in
// internal/resources/security.go.
type SecurityOverrides struct {
	// Pod-level overrides (e.g. RunAsUser, FSGroup, SupplementalGroups,
	// custom seccomp profile path). Cannot disable RunAsNonRoot or use
	// root UID 0.
	// +optional
	Pod *corev1.PodSecurityContext `json:"pod,omitempty"`

	// Container-level overrides applied to the workload's main container
	// only. Init containers and operator-injected sidecars (mcp-gateway,
	// token-injector) always use the restricted defaults. Cannot enable
	// privilege escalation, disable RO rootfs, disable RunAsNonRoot,
	// or add capabilities.
	// +optional
	Container *corev1.SecurityContext `json:"container,omitempty"`

	// AutomountServiceAccountToken opts the workload's pod into
	// receiving its ServiceAccount's API token. Default false; set
	// to true only for workloads that legitimately call the Kubernetes
	// API at runtime.
	// +optional
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`
}

// Condition type emitted on Agent / AgentTool / Channel when one or more
// security override fields have been clamped to the safety floor.
const ConditionSecurityPolicyViolations = "SecurityPolicyViolations"
