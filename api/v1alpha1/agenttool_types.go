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

// AgentToolPhase describes the current phase of an AgentTool.
type AgentToolPhase string

const (
	AgentToolPhasePending   AgentToolPhase = "Pending"
	AgentToolPhaseDeploying AgentToolPhase = "Deploying" // mcpServer source only
	AgentToolPhaseReady     AgentToolPhase = "Ready"
	AgentToolPhaseFailed    AgentToolPhase = "Failed"
)

// AgentToolSpec defines the desired state of AgentTool.
// Exactly one source block must be set.
type AgentToolSpec struct {

	// ================================================================
	// METADATA
	// ================================================================

	// Human-friendly description shown in the console UI.
	// +optional
	Description string `json:"description,omitempty"`

	// Category for console UI grouping (e.g. "infrastructure", "coding", "data").
	// +optional
	Category string `json:"category,omitempty"`

	// UIHint tells the console which branded card renderer to use for this tool's
	// call results. When set, this value is passed through to every FEP tool_result
	// event as metadata.ui, overriding heuristic detection.
	// Known values: "kubernetes-resources", "helm-release", "terminal", "code", "diff",
	// "file-tree", "search-results", "file-created", "web-fetch", "agent-run".
	// +optional
	UIHint string `json:"uiHint,omitempty"`

	// ================================================================
	// SOURCE — exactly one must be set
	// ================================================================

	// OCI artifact containing an MCP tool server binary.
	// Operator pulls via crane init container, runtime launches as stdio MCP server.
	// +optional
	OCI *OCIToolSource `json:"oci,omitempty"`

	// ConfigMap containing a tool script (e.g. index.js).
	// Mounted as a volume at /tools/<name>.
	// +optional
	ConfigMap *ConfigMapToolSource `json:"configMap,omitempty"`

	// Inline tool content (< 4KB, prototyping only).
	// Written to a ConfigMap by the operator, mounted at /tools/<name>.
	// +optional
	Inline *InlineToolSource `json:"inline,omitempty"`

	// MCP server deployed by the operator (absorbs MCPServer deploy mode).
	// Operator creates Deployment+Service, agents connect via gateway sidecar.
	// +optional
	MCPServer *MCPServerToolSource `json:"mcpServer,omitempty"`

	// External MCP endpoint (absorbs MCPServer external mode).
	// Operator health-checks, agents connect via gateway sidecar.
	// +optional
	MCPEndpoint *MCPEndpointToolSource `json:"mcpEndpoint,omitempty"`

	// OCI artifact containing skill markdown (system prompt extensions).
	// Pulled via crane init container, mounted as context files.
	// +optional
	Skill *SkillToolSource `json:"skill,omitempty"`

	// ================================================================
	// DEFAULT PERMISSIONS
	// Agent-level bindings can override these.
	// ================================================================

	// Default permission configuration for this tool.
	// +optional
	DefaultPermissions *ToolPermissions `json:"defaultPermissions,omitempty"`
}

// ================================================================
// Source types
// ================================================================

// OCIToolSource pulls an MCP tool server from an OCI registry.
type OCIToolSource struct {
	// Full OCI reference (e.g. ghcr.io/samyn92/agent-tools/kubectl:1.0.0).
	Ref string `json:"ref"`
	// Optional digest for pinning.
	// +optional
	Digest string `json:"digest,omitempty"`
	// Pull policy: Always, IfNotPresent, Never.
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	PullPolicy string `json:"pullPolicy,omitempty"`
	// Pull secret for private registries.
	// +optional
	PullSecret *SecretKeyRef `json:"pullSecret,omitempty"`
}

// ConfigMapToolSource mounts a tool script from a ConfigMap.
type ConfigMapToolSource struct {
	// ConfigMap name.
	Name string `json:"name"`
	// Key within the ConfigMap.
	Key string `json:"key"`
}

// InlineToolSource embeds tool content directly (< 4KB).
type InlineToolSource struct {
	// Tool script content.
	Content string `json:"content"`
}

// MCPServerToolSource deploys an MCP server (absorbs MCPServer deploy mode).
type MCPServerToolSource struct {
	// Container image for the MCP server.
	Image string `json:"image"`
	// Port the MCP server listens on.
	// +optional
	// +kubebuilder:default=8080
	Port int32 `json:"port,omitempty"`
	// Override command for the container.
	// +optional
	Command []string `json:"command,omitempty"`
	// Plain-text environment variables.
	// +optional
	Env map[string]string `json:"env,omitempty"`
	// Secret-backed environment variables.
	// +optional
	Secrets []SecretEnvVar `json:"secrets,omitempty"`
	// ServiceAccount for the MCP server pod.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// Compute resources.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// Health check configuration.
	// +optional
	HealthCheck *MCPHealthCheck `json:"healthCheck,omitempty"`
	// Security overrides for the MCP server pod. See SecurityOverrides.
	// +optional
	Security *SecurityOverrides `json:"security,omitempty"`
}

// MCPEndpointToolSource points to an external MCP server (absorbs MCPServer external mode).
type MCPEndpointToolSource struct {
	// URL of the external MCP server.
	URL string `json:"url"`
	// Transport type: sse or streamable-http.
	// +optional
	// +kubebuilder:default=sse
	// +kubebuilder:validation:Enum=sse;streamable-http
	Transport string `json:"transport,omitempty"`
	// Static headers.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`
	// OAuth configuration.
	// +optional
	OAuth *MCPOAuthConfig `json:"oauth,omitempty"`
	// Health check configuration.
	// +optional
	HealthCheck *MCPHealthCheck `json:"healthCheck,omitempty"`
}

// SkillToolSource pulls skill markdown from an OCI registry.
type SkillToolSource struct {
	// Full OCI reference for the skill package.
	Ref string `json:"ref"`
	// Optional digest for pinning.
	// +optional
	Digest string `json:"digest,omitempty"`
	// Pull policy.
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	PullPolicy string `json:"pullPolicy,omitempty"`
	// Pull secret for private registries.
	// +optional
	PullSecret *SecretKeyRef `json:"pullSecret,omitempty"`
}

// ToolPermissions defines default permission settings for a tool.
type ToolPermissions struct {
	// Require user approval before execution.
	// +optional
	RequireApproval bool `json:"requireApproval,omitempty"`
	// Deny/allow mode for MCP tool calls (mcpServer/mcpEndpoint sources only).
	// +optional
	// +kubebuilder:validation:Enum=deny;allow
	Mode string `json:"mode,omitempty"`
	// Rules for deny/allow filtering.
	// +optional
	Rules []string `json:"rules,omitempty"`
}

// ================================================================
// Status
// ================================================================

// AgentToolStatus defines the observed state of AgentTool.
type AgentToolStatus struct {
	// Current phase: Pending, Deploying, Ready, Failed.
	// +optional
	Phase AgentToolPhase `json:"phase,omitempty"`

	// Source type detected from spec (oci, configMap, inline, mcpServer, mcpEndpoint, skill).
	// +optional
	SourceType string `json:"sourceType,omitempty"`

	// Service URL for mcpServer/mcpEndpoint sources.
	// +optional
	ServiceURL string `json:"serviceURL,omitempty"`

	// Discovered MCP tools exposed by this tool server.
	// Populated by the operator at reconcile time via MCP ListTools introspection.
	// +optional
	Tools []DiscoveredTool `json:"tools,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DiscoveredTool describes a single MCP tool discovered from a tool server.
// Populated by the operator during reconciliation via ListTools introspection.
type DiscoveredTool struct {
	// MCP tool name (e.g. "kube_find", "kube_health").
	Name string `json:"name"`

	// Tool description from the MCP server.
	// +optional
	Description string `json:"description,omitempty"`

	// Input schema as a JSON string (the MCP tool's inputSchema object).
	// +optional
	InputSchema string `json:"inputSchema,omitempty"`
}

// Condition types for AgentTool.
const (
	AgentToolConditionReady = "Ready"
)

// ================================================================
// Root types
// ================================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=agtool
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.status.sourceType`
// +kubebuilder:printcolumn:name="Category",type=string,JSONPath=`.spec.category`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.serviceURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTool is the Schema for the agenttools API.
// Unified tool catalog entry. Defines a tool by what it does, not how
// it's delivered. The source block (oci, configMap, inline, mcpServer,
// mcpEndpoint, skill) determines how the operator provisions the tool.
type AgentTool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentToolSpec   `json:"spec,omitempty"`
	Status AgentToolStatus `json:"status,omitempty"`
}

// DetectSourceType returns the source type string based on which source block is set.
func (t *AgentTool) DetectSourceType() string {
	switch {
	case t.Spec.OCI != nil:
		return "oci"
	case t.Spec.ConfigMap != nil:
		return "configMap"
	case t.Spec.Inline != nil:
		return "inline"
	case t.Spec.MCPServer != nil:
		return "mcpServer"
	case t.Spec.MCPEndpoint != nil:
		return "mcpEndpoint"
	case t.Spec.Skill != nil:
		return "skill"
	default:
		return ""
	}
}

// IsMCPSource returns true if the tool is an MCP server or endpoint.
func (t *AgentTool) IsMCPSource() bool {
	return t.Spec.MCPServer != nil || t.Spec.MCPEndpoint != nil
}

// +kubebuilder:object:root=true

// AgentToolList contains a list of AgentTool.
type AgentToolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTool{}, &AgentToolList{})
}
