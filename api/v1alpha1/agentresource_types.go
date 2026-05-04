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

// AgentResourceKind defines the type of resource.
// +kubebuilder:validation:Enum=github-repo;github-org;gitlab-project;gitlab-group;git-repo;s3-bucket;documentation
type AgentResourceKind string

const (
	AgentResourceKindGitHubRepo    AgentResourceKind = "github-repo"
	AgentResourceKindGitHubOrg     AgentResourceKind = "github-org"
	AgentResourceKindGitLabProject AgentResourceKind = "gitlab-project"
	AgentResourceKindGitLabGroup   AgentResourceKind = "gitlab-group"
	AgentResourceKindGitRepo       AgentResourceKind = "git-repo"
	AgentResourceKindS3Bucket      AgentResourceKind = "s3-bucket"
	AgentResourceKindDocumentation AgentResourceKind = "documentation"
)

// AgentResourcePhase describes the current phase of an AgentResource.
type AgentResourcePhase string

const (
	AgentResourcePhasePending AgentResourcePhase = "Pending"
	AgentResourcePhaseReady   AgentResourcePhase = "Ready"
	AgentResourcePhaseFailed  AgentResourcePhase = "Failed"
)

// AgentResourceSpec defines the desired state of AgentResource.
type AgentResourceSpec struct {

	// ====================================================================
	// IDENTITY
	// ====================================================================

	// Kind of resource (e.g. github-repo, gitlab-group, git-repo).
	// +kubebuilder:validation:Required
	Kind AgentResourceKind `json:"kind"`

	// Human-friendly display name shown in the console UI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Optional description of the resource for UI tooltips.
	// +optional
	Description string `json:"description,omitempty"`

	// ====================================================================
	// CREDENTIALS
	// ====================================================================

	// Optional credentials for accessing the resource.
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

// AgentResourceStatus defines the observed state of AgentResource.
type AgentResourceStatus struct {
	// Current phase: Pending, Ready, Failed.
	// +optional
	Phase AgentResourcePhase `json:"phase,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for AgentResource.
const (
	// AgentResourceConditionReady indicates the resource is validated and usable.
	AgentResourceConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ares
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.kind`
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentResource is the Schema for the agentresources API.
// A declarative catalog entry for an external resource (Git repo, GitLab group,
// S3 bucket, documentation, etc.) that agents can work with.
// Agents bind to resources via spec.resourceBindings, and users can select them
// in the console UI to scope prompts.
type AgentResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentResourceSpec   `json:"spec,omitempty"`
	Status AgentResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentResourceList contains a list of AgentResource.
type AgentResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentResource `json:"items"`
}

