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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IntegrationKind defines the type of integration.
// +kubebuilder:validation:Enum=github-repo;github-org;gitlab-project;gitlab-group;git-repo;s3-bucket;documentation
type IntegrationKind string

const (
	IntegrationKindGitHubRepo    IntegrationKind = "github-repo"
	IntegrationKindGitHubOrg     IntegrationKind = "github-org"
	IntegrationKindGitLabProject IntegrationKind = "gitlab-project"
	IntegrationKindGitLabGroup   IntegrationKind = "gitlab-group"
	IntegrationKindGitRepo       IntegrationKind = "git-repo"
	IntegrationKindS3Bucket      IntegrationKind = "s3-bucket"
	IntegrationKindDocumentation IntegrationKind = "documentation"
)

// IntegrationPhase describes the current phase of an Integration.
type IntegrationPhase string

const (
	IntegrationPhasePending IntegrationPhase = "Pending"
	IntegrationPhaseReady   IntegrationPhase = "Ready"
	IntegrationPhaseFailed  IntegrationPhase = "Failed"
)

// IntegrationTrigger defines an event-driven trigger that creates AgentRuns
// when matching events occur on the platform.
type IntegrationTrigger struct {
	// Event type to match (e.g. "merge_request", "pull_request", "push", "issue").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	On string `json:"on"`

	// Actions to filter on (e.g. ["open", "update", "merge"]).
	// Empty means all actions.
	// +optional
	Actions []string `json:"actions,omitempty"`

	// Labels to filter on. Event must have at least one matching label.
	// Empty means no label filtering.
	// +optional
	Labels []string `json:"labels,omitempty"`

	// Name of the Agent CR to trigger.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// Go text/template rendered with event data as the prompt for the AgentRun.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Prompt string `json:"prompt"`

	// Git workspace configuration for the triggered AgentRun.
	// If set, the agent gets a cloned workspace with a feature branch.
	// +optional
	Git *IntegrationTriggerGit `json:"git,omitempty"`
}

// IntegrationTriggerGit configures the git workspace for triggered AgentRuns.
type IntegrationTriggerGit struct {
	// Branch name template. Supports Go template with event data.
	// Example: "agent/review-{{.object_attributes.iid}}"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Base branch for the PR/MR target. Defaults to the integration's default branch.
	// +optional
	BaseBranch string `json:"baseBranch,omitempty"`
}

// IntegrationSpec defines the desired state of Integration.
type IntegrationSpec struct {

	// ====================================================================
	// IDENTITY
	// ====================================================================

	// Kind of integration (e.g. github-repo, gitlab-project, git-repo).
	// +kubebuilder:validation:Required
	Kind IntegrationKind `json:"kind"`

	// Human-friendly display name shown in the console UI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Optional description for UI tooltips.
	// +optional
	Description string `json:"description,omitempty"`

	// ====================================================================
	// CREDENTIALS
	// ====================================================================

	// Optional credentials for accessing the platform.
	// The secret key usage is kind-specific (e.g. API token for GitHub/GitLab,
	// SSH key for git, AWS credentials for S3).
	// +optional
	Credentials *SecretKeyRef `json:"credentials,omitempty"`

	// ====================================================================
	// KIND-SPECIFIC CONFIGURATION
	// Exactly one block must match the kind field.
	// ====================================================================

	// GitHub repository configuration (kind: github-repo).
	// +optional
	GitHub *GitHubResourceConfig `json:"github,omitempty"`

	// GitHub organization configuration (kind: github-org).
	// +optional
	GitHubOrg *GitHubOrgResourceConfig `json:"githubOrg,omitempty"`

	// GitLab project configuration (kind: gitlab-project).
	// +optional
	GitLab *GitLabResourceConfig `json:"gitlab,omitempty"`

	// GitLab group configuration (kind: gitlab-group).
	// +optional
	GitLabGroup *GitLabGroupResourceConfig `json:"gitlabGroup,omitempty"`

	// Plain git repository configuration (kind: git-repo).
	// +optional
	Git *GitResourceConfig `json:"git,omitempty"`

	// S3 bucket configuration (kind: s3-bucket).
	// +optional
	S3 *S3ResourceConfig `json:"s3,omitempty"`

	// Documentation configuration (kind: documentation).
	// +optional
	Documentation *DocumentationResourceConfig `json:"documentation,omitempty"`

	// ====================================================================
	// TRIGGERS
	// ====================================================================

	// Event-driven triggers. When a matching event occurs on the platform,
	// the operator creates an AgentRun for the specified agent.
	// +optional
	Triggers []IntegrationTrigger `json:"triggers,omitempty"`
}

// ====================================================================
// Kind-specific config structs
// ====================================================================

// GitHubResourceConfig configures a GitHub repository resource.
type GitHubResourceConfig struct {
	// Repository owner (user or org).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Owner string `json:"owner"`

	// Repository name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Default branch to use (e.g. "main"). If unset, uses the repo default.
	// +optional
	DefaultBranch string `json:"defaultBranch,omitempty"`

	// GitHub API base URL. Defaults to https://api.github.com for github.com.
	// Set this for GitHub Enterprise.
	// +optional
	APIURL string `json:"apiURL,omitempty"`
}

// GitHubOrgResourceConfig configures a GitHub organization resource.
type GitHubOrgResourceConfig struct {
	// Organization name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Org string `json:"org"`

	// Optional filter to include only specific repos (glob patterns).
	// +optional
	RepoFilter []string `json:"repoFilter,omitempty"`

	// GitHub API base URL. Defaults to https://api.github.com for github.com.
	// +optional
	APIURL string `json:"apiURL,omitempty"`
}

// GitLabResourceConfig configures a GitLab project resource.
type GitLabResourceConfig struct {
	// GitLab base URL (e.g. https://gitlab.com).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BaseURL string `json:"baseURL"`

	// Project path (e.g. "group/subgroup/project").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// Default branch to use. If unset, uses the project default.
	// +optional
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

// GitLabGroupResourceConfig configures a GitLab group resource.
type GitLabGroupResourceConfig struct {
	// GitLab base URL (e.g. https://gitlab.com).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BaseURL string `json:"baseURL"`

	// Group path (e.g. "myorg" or "myorg/subgroup").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// Optional filter to include only specific projects within the group.
	// +optional
	Projects []string `json:"projects,omitempty"`
}

// GitResourceConfig configures a plain git repository resource.
type GitResourceConfig struct {
	// Git clone URL (HTTPS or SSH).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Default branch. If unset, uses the repo default.
	// +optional
	Branch string `json:"branch,omitempty"`

	// SSH private key secret (for SSH URLs). Overrides credentials if set.
	// +optional
	SSHKeySecret *SecretKeyRef `json:"sshKeySecret,omitempty"`
}

// S3ResourceConfig configures an S3-compatible bucket resource.
type S3ResourceConfig struct {
	// Bucket name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Bucket string `json:"bucket"`

	// Region.
	// +optional
	Region string `json:"region,omitempty"`

	// Endpoint URL for S3-compatible storage (e.g. MinIO).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Prefix to scope access within the bucket.
	// +optional
	Prefix string `json:"prefix,omitempty"`
}

// DocumentationResourceConfig configures a documentation resource.
type DocumentationResourceConfig struct {
	// URLs to documentation pages.
	// +optional
	URLs []string `json:"urls,omitempty"`

	// ConfigMap containing documentation content (e.g. markdown files).
	// +optional
	ConfigMapRef *SecretKeyRef `json:"configMapRef,omitempty"`
}

// ====================================================================
// Status
// ====================================================================

// IntegrationStatus defines the observed state of Integration.
type IntegrationStatus struct {
	// Current phase: Pending, Ready, Failed.
	// +optional
	Phase IntegrationPhase `json:"phase,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Integration.
const (
	// IntegrationConditionReady indicates the integration is validated and usable.
	IntegrationConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=intg
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.kind`
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Triggers",type=integer,JSONPath=`.spec.triggers`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Integration is the Schema for the integrations API.
// A declarative connection to an external platform (Git repo, GitLab project,
// GitHub repo, S3 bucket, etc.) that agents can work with. Includes optional
// event-driven triggers that automatically create AgentRuns when matching
// events occur on the platform.
type Integration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IntegrationSpec   `json:"spec,omitempty"`
	Status IntegrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IntegrationList contains a list of Integration.
type IntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Integration `json:"items"`
}
