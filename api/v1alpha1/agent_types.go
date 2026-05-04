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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentMode defines the agent lifecycle mode.
// +kubebuilder:validation:Enum=daemon;task
type AgentMode string

const (
	AgentModeDaemon AgentMode = "daemon"
	AgentModeTask   AgentMode = "task"
)

// AgentPhase describes the current phase of an Agent.
type AgentPhase string

const (
	AgentPhasePending AgentPhase = "Pending"
	AgentPhaseRunning AgentPhase = "Running" // daemon
	AgentPhaseReady   AgentPhase = "Ready"   // task
	AgentPhaseFailed  AgentPhase = "Failed"
)

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {

	// ====================================================================
	// MODE
	// ====================================================================

	// Mode: daemon (Deployment+PVC+Service) or task (Job template).
	// +kubebuilder:validation:Required
	Mode AgentMode `json:"mode"`

	// ====================================================================
	// RUNTIME
	// ====================================================================

	// Container image for the Fantasy agent runtime.
	// +optional
	// +kubebuilder:default="ghcr.io/samyn92/agent-runtime-fantasy:latest"
	Image string `json:"image,omitempty"`

	// Image pull policy.
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Built-in tools to enable.
	// Valid: bash, read, edit, write, grep, ls, glob, fetch.
	// Omit for all defaults; set to empty list [] to disable all built-in tools.
	// +optional
	BuiltinTools []string `json:"builtinTools"`

	// Temperature for model calls (0.0 - 2.0).
	// +optional
	Temperature *float64 `json:"temperature,omitempty"`

	// Maximum output tokens per model call.
	// +optional
	MaxOutputTokens *int64 `json:"maxOutputTokens,omitempty"`

	// Maximum agent loop steps (safety limit to prevent infinite loops).
	// +optional
	// +kubebuilder:default=100
	MaxSteps *int `json:"maxSteps,omitempty"`

	// ====================================================================
	// MODEL
	// ====================================================================

	// Primary model in provider/model format (e.g. anthropic/claude-sonnet-4-20250514).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// Preferred provider name for model resolution.
	// When the model string does not include a provider prefix, this value
	// is used to select which configured provider handles the request.
	// +optional
	PrimaryProvider string `json:"primaryProvider,omitempty"`

	// Fast/cheap model used for auto-titling sessions (e.g. openai/gpt-4o-mini).
	// Only relevant for daemon-mode agents. If unset, the primary model is used.
	// +optional
	TitleModel string `json:"titleModel,omitempty"`

	// Provider references. Each entry names a Provider CR in the same namespace.
	// At least one providerRef is required.
	// +kubebuilder:validation:MinItems=1
	ProviderRefs []ProviderBinding `json:"providerRefs"`

	// Fallback models tried in order if the primary model fails.
	// Each must reference a provider listed in providers.
	// +optional
	FallbackModels []string `json:"fallbackModels,omitempty"`

	// ====================================================================
	// IDENTITY
	// ====================================================================

	// System prompt injected at the start of every session.
	// +optional
	SystemPrompt string `json:"systemPrompt,omitempty"`

	// Context files loaded from ConfigMaps (e.g. AGENTS.md).
	// +optional
	ContextFiles []ContextFileRef `json:"contextFiles,omitempty"`

	// ====================================================================
	// DELEGATION
	// ====================================================================

	// Delegation controls how this agent delegates work to other agents.
	// The team list IS the access control — only listed agents can be delegated to.
	// When set, the operator generates a delegation protocol that is injected
	// as a separate platform protocol section (never appended to systemPrompt).
	// Omit entirely to get no delegation protocol injection.
	// +optional
	Delegation *DelegationSpec `json:"delegation,omitempty"`

	// ====================================================================
	// TOOLS (unified via AgentTool CRs)
	// ====================================================================

	// Tool bindings referencing AgentTool CRs. Each binding names an
	// AgentTool and allows per-agent permission overrides.
	// +optional
	Tools []AgentToolBinding `json:"tools,omitempty"`

	// Tools that require user approval before execution (permission gate).
	// Each entry is a tool name. If empty, all tools run automatically.
	// +optional
	PermissionTools []string `json:"permissionTools,omitempty"`

	// Enable the built-in "question" tool that lets the agent ask the user
	// interactive questions during execution.
	// +optional
	EnableQuestionTool bool `json:"enableQuestionTool,omitempty"`

	// ====================================================================
	// ENVIRONMENT
	// ====================================================================

	// Plain-text environment variables.
	// +optional
	Env map[string]string `json:"env,omitempty"`

	// Secret-backed environment variables.
	// +optional
	Secrets []SecretEnvVar `json:"secrets,omitempty"`

	// ====================================================================
	// STORAGE (daemon only, ignored for task)
	// ====================================================================

	// Persistent storage for daemon agents (PVC, RWO).
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// ====================================================================
	// MEMORY (Engram integration)
	// ====================================================================

	// Memory configuration for the Engram shared memory system.
	// When set, the runtime injects recent context and enables
	// memory MCP tools (mem_save, mem_search, etc.).
	// +optional
	Memory *MemorySpec `json:"memory,omitempty"`

	// ====================================================================
	// RESOURCES (accessible external resources)
	// ====================================================================

	// External resources (repos, groups, etc.) bound to this agent.
	// Users can select bound resources in the console UI to scope prompts.
	// +optional
	ResourceBindings []AgentResourceBinding `json:"resourceBindings,omitempty"`

	// ====================================================================
	// TOOL HOOKS (defense-in-depth)
	// ====================================================================

	// Runtime security hooks for tool calls.
	// +optional
	ToolHooks *ToolHooksSpec `json:"toolHooks,omitempty"`

	// ====================================================================
	// SCHEDULE
	// ====================================================================

	// Cron schedule for creating periodic AgentRuns.
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// Prompt used when schedule triggers an AgentRun.
	// +optional
	SchedulePrompt string `json:"schedulePrompt,omitempty"`

	// ====================================================================
	// CONCURRENCY
	// ====================================================================

	// Concurrency control for parallel AgentRun execution.
	// +optional
	Concurrency *ConcurrencySpec `json:"concurrency,omitempty"`

	// ====================================================================
	// INFRASTRUCTURE
	// ====================================================================

	// Compute resources for the agent container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ServiceAccount for the agent pod.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Timeout for task jobs or per-prompt timeout for daemons (e.g. "10m").
	// +optional
	// +kubebuilder:default="10m"
	Timeout string `json:"timeout,omitempty"`

	// Network policy configuration.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`

	// Security overrides for the agent pod. The operator applies a
	// restricted-by-default SecurityContext to every pod it builds; this
	// field lets users override fields the floor permits (UID/GID, FSGroup,
	// seccomp profile path, automount opt-in). Any override that would
	// weaken the restricted Pod Security Standard is silently clamped and
	// reported on .status.conditions[type=SecurityPolicyViolations].
	// +optional
	Security *SecurityOverrides `json:"security,omitempty"`
}

// AgentStatus defines the observed state of Agent.
type AgentStatus struct {
	// Current phase: Pending, Running (daemon), Ready (task), Failed.
	// +optional
	Phase AgentPhase `json:"phase,omitempty"`

	// Service URL for daemon agents (e.g. http://agent.ns.svc:4096).
	// +optional
	ServiceURL string `json:"serviceURL,omitempty"`

	// Number of ready replicas (daemon only).
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Name of the PVC created for daemon agents.
	// +optional
	StoragePVC string `json:"storagePVC,omitempty"`

	// Currently active model (may differ from spec.model if fallback triggered).
	// +optional
	ActiveModel string `json:"activeModel,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Agent.
const (
	// AgentConditionReady indicates the agent is fully operational.
	AgentConditionReady = "Ready"
	// AgentConditionToolsReady indicates all bound AgentTools are Ready.
	AgentConditionToolsReady = "ToolsReady"
	// AgentConditionProvidersReady indicates all LLM providers are configured.
	AgentConditionProvidersReady = "ProvidersReady"
	// AgentConditionResourcesReady indicates all resource bindings are resolved and ready.
	AgentConditionResourcesReady = "ResourcesReady"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ag
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent is the Schema for the agents API.
// One CRD for all agents. mode controls lifecycle:
// daemon = Deployment + PVC + Service (always running).
// task = Job template (one prompt, exits).
// The agent runtime is powered by the Charm Fantasy SDK (Go).
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec,omitempty"`
	Status AgentStatus `json:"status,omitempty"`
}

// RuntimeImage returns the container image, falling back to the default.
func (a *Agent) RuntimeImage() string {
	if a.Spec.Image != "" {
		return a.Spec.Image
	}
	return DefaultFantasyImage
}

// RuntimeImagePullPolicy returns the image pull policy, falling back to IfNotPresent.
func (a *Agent) RuntimeImagePullPolicy() corev1.PullPolicy {
	if a.Spec.ImagePullPolicy != "" {
		return a.Spec.ImagePullPolicy
	}
	return corev1.PullIfNotPresent
}

// BuiltinToolCount returns the number of built-in tools configured.
func (a *Agent) BuiltinToolCount() int {
	return len(a.Spec.BuiltinTools)
}

// Default image for the Fantasy runtime.
const DefaultFantasyImage = "ghcr.io/samyn92/agent-runtime-fantasy:latest"

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}
