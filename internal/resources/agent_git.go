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

package resources

import (
	"fmt"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	// Container images for the platform-specific MCP tool servers.
	// These are real container images (with rootfs, PATH, etc.) built from
	// each server's Dockerfile — NOT the OCI artifacts pushed by `agent-tools push`.
	// The "-server" suffix distinguishes container images from OCI artifacts.
	//
	// Note: git operations (clone, commit, push, etc.) are now handled by
	// go-git built into the runtime — no mcp-git sidecar needed.
	// Platform tools (GitHub/GitLab) are loaded via init container + stdio exec
	// — no gateway sidecar needed.
	GitHubToolImage = "ghcr.io/samyn92/agent-tools/github-server:latest"
	GitLabToolImage = "ghcr.io/samyn92/agent-tools/gitlab-server:latest"

	// Git provider constants.
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
	ProviderGit    = "git"
)

// GitWorkspaceConfig holds resolved git workspace configuration for a task agent run.
type GitWorkspaceConfig struct {
	// Provider: "github", "gitlab", or "git".
	Provider string
	// HTTPS clone URL.
	CloneURL string
	// Feature branch name.
	Branch string
	// Base branch for PR/MR target.
	BaseBranch string
	// Credential secret reference (token).
	Credentials *agentsv1alpha1.SecretKeyRef

	// GitHub-specific
	GitHubOwner  string
	GitHubRepo   string
	GitHubAPIURL string

	// GitLab-specific
	GitLabProject string
	GitLabBaseURL string
}

// ResolveGitWorkspace extracts git workspace configuration from an Integration.
func ResolveGitWorkspace(
	gitSpec *agentsv1alpha1.AgentRunGitSpec,
	resource *agentsv1alpha1.Integration,
) (*GitWorkspaceConfig, error) {
	cfg := &GitWorkspaceConfig{
		Branch:      gitSpec.Branch,
		BaseBranch:  gitSpec.BaseBranch,
		Credentials: resource.Spec.Credentials,
	}

	switch resource.Spec.Kind {
	case agentsv1alpha1.IntegrationKindGitHubRepo:
		if resource.Spec.GitHub == nil {
			return nil, fmt.Errorf("integration %q kind is github-repo but spec.github is nil", resource.Name)
		}
		gh := resource.Spec.GitHub
		cfg.Provider = ProviderGitHub
		cfg.CloneURL = fmt.Sprintf("https://github.com/%s/%s.git", gh.Owner, gh.Repo)
		cfg.GitHubOwner = gh.Owner
		cfg.GitHubRepo = gh.Repo
		cfg.GitHubAPIURL = gh.APIURL
		if cfg.BaseBranch == "" {
			cfg.BaseBranch = gh.DefaultBranch
		}
		if cfg.GitHubAPIURL != "" {
			// GitHub Enterprise: clone URL uses the API host
			cfg.CloneURL = fmt.Sprintf("%s/%s/%s.git", cfg.GitHubAPIURL, gh.Owner, gh.Repo)
		}

	case agentsv1alpha1.IntegrationKindGitLabProject:
		if resource.Spec.GitLab == nil {
			return nil, fmt.Errorf("integration %q kind is gitlab-project but spec.gitlab is nil", resource.Name)
		}
		gl := resource.Spec.GitLab
		cfg.Provider = ProviderGitLab
		cfg.CloneURL = fmt.Sprintf("%s/%s.git", gl.BaseURL, gl.Project)
		cfg.GitLabProject = gl.Project
		cfg.GitLabBaseURL = gl.BaseURL
		if cfg.BaseBranch == "" {
			cfg.BaseBranch = gl.DefaultBranch
		}

	case agentsv1alpha1.IntegrationKindGitRepo:
		if resource.Spec.Git == nil {
			return nil, fmt.Errorf("integration %q kind is git-repo but spec.git is nil", resource.Name)
		}
		cfg.Provider = ProviderGit
		cfg.CloneURL = resource.Spec.Git.URL
		if cfg.BaseBranch == "" {
			cfg.BaseBranch = resource.Spec.Git.Branch
		}

	default:
		return nil, fmt.Errorf("integration %q kind %q is not a git resource", resource.Name, resource.Spec.Kind)
	}

	// Default base branch
	if cfg.BaseBranch == "" {
		cfg.BaseBranch = "main"
	}

	return cfg, nil
}

// GitEnvVars returns environment variables for the task agent runtime to set up
// the git workspace (clone, branch, auth).
func (g *GitWorkspaceConfig) GitEnvVars() []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "GIT_PROVIDER", Value: g.Provider},
		{Name: "GIT_REPO_URL", Value: g.CloneURL},
		{Name: "GIT_BRANCH", Value: g.Branch},
		{Name: "GIT_BASE_BRANCH", Value: g.BaseBranch},
	}

	// Provider-specific env vars for the MCP tools
	switch g.Provider {
	case ProviderGitHub:
		env = append(env,
			corev1.EnvVar{Name: "GIT_OWNER", Value: g.GitHubOwner},
			corev1.EnvVar{Name: "GIT_REPO", Value: g.GitHubRepo},
		)
		if g.GitHubAPIURL != "" {
			env = append(env, corev1.EnvVar{Name: "GITHUB_API_URL", Value: g.GitHubAPIURL})
		}
	case ProviderGitLab:
		env = append(env,
			corev1.EnvVar{Name: "GIT_PROJECT", Value: g.GitLabProject},
			corev1.EnvVar{Name: "GITLAB_URL", Value: g.GitLabBaseURL},
		)
	}

	// Credential env var (GH_TOKEN or GITLAB_TOKEN) from Secret
	if g.Credentials != nil {
		tokenEnvName := "GIT_TOKEN"
		switch g.Provider {
		case ProviderGitHub:
			tokenEnvName = "GH_TOKEN"
		case ProviderGitLab:
			tokenEnvName = "GITLAB_TOKEN"
		}
		env = append(env, corev1.EnvVar{
			Name: tokenEnvName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: g.Credentials.Name},
					Key:                  g.Credentials.Key,
				},
			},
		})
		// Also set GIT_TOKEN for the credential helper (used by git clone)
		if tokenEnvName != "GIT_TOKEN" {
			env = append(env, corev1.EnvVar{
				Name: "GIT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: g.Credentials.Name},
						Key:                  g.Credentials.Key,
					},
				},
			})
		}
	}

	return env
}

// GitToolEntries returns ToolEntry items for the runtime config so it discovers
// git platform tools via loadOCITools() (stdio exec, no gateway sidecar).
// The init containers copy the tool binary + manifest.json into /tools/<provider>.
func (g *GitWorkspaceConfig) GitToolEntries() []ToolEntry {
	var entries []ToolEntry

	switch g.Provider {
	case ProviderGitHub:
		entries = append(entries, ToolEntry{
			Name:        "github",
			Path:        MountTools + "/github",
			Description: "GitHub API — PRs, issues, branches, checks, workflows",
			Category:    "git",
			UIHint:      "github",
		})
	case ProviderGitLab:
		entries = append(entries, ToolEntry{
			Name:        "gitlab",
			Path:        MountTools + "/gitlab",
			Description: "GitLab API — MRs, issues, pipelines, projects",
			Category:    "git",
			UIHint:      "gitlab",
		})
	}

	return entries
}

// GitToolVolumes returns volumes needed for the platform-specific tool binaries.
// Each provider gets an emptyDir where the init container copies the binary + manifest.
func (g *GitWorkspaceConfig) GitToolVolumes() []corev1.Volume {
	var volumes []corev1.Volume
	switch g.Provider {
	case ProviderGitHub:
		volumes = append(volumes, corev1.Volume{
			Name: "tool-github", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	case ProviderGitLab:
		volumes = append(volumes, corev1.Volume{
			Name: "tool-gitlab", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}
	return volumes
}

// GitToolVolumeMounts returns volume mounts for the runtime container so it
// can access the tool binary + manifest copied by the init container.
func (g *GitWorkspaceConfig) GitToolVolumeMounts() []corev1.VolumeMount {
	var mounts []corev1.VolumeMount
	switch g.Provider {
	case ProviderGitHub:
		mounts = append(mounts, corev1.VolumeMount{
			Name: "tool-github", MountPath: MountTools + "/github", ReadOnly: true,
		})
	case ProviderGitLab:
		mounts = append(mounts, corev1.VolumeMount{
			Name: "tool-gitlab", MountPath: MountTools + "/gitlab", ReadOnly: true,
		})
	}
	return mounts
}

// GitToolInitContainers returns init containers that copy the tool binary and
// manifest.json from the tool server image into a shared emptyDir volume.
// The runtime's loadOCITools() reads manifest.json to find the binary name,
// then spawns it via stdio — no gateway or HTTP hop needed.
func (g *GitWorkspaceConfig) GitToolInitContainers() []corev1.Container {
	var inits []corev1.Container
	switch g.Provider {
	case ProviderGitHub:
		inits = append(inits, buildToolCopyInitContainer("copy-tool-github", GitHubToolImage, "tool-github", "mcp-github"))
	case ProviderGitLab:
		inits = append(inits, buildToolCopyInitContainer("copy-tool-gitlab", GitLabToolImage, "tool-gitlab", "mcp-gitlab"))
	}
	return inits
}

// buildToolCopyInitContainer creates an init container that copies the tool
// binary and manifest.json from the tool server image into a shared volume.
// Layout after copy: /tools/<provider>/manifest.json, /tools/<provider>/mcp-<provider>
func buildToolCopyInitContainer(name, image, volumeName, binaryName string) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   image,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{fmt.Sprintf("cp /bin/%s /out/%s && cp /manifest.json /out/manifest.json", binaryName, binaryName)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeName, MountPath: "/out"},
		},
	}
}
