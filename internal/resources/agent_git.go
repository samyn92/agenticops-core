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
	// Container images for the git MCP tool server sidecars.
	// These are real container images (with rootfs, PATH, etc.) built from
	// each server's Dockerfile — NOT the OCI artifacts pushed by `agent-tools push`.
	// The "-server" suffix distinguishes container images from OCI artifacts.
	GitToolImage    = "ghcr.io/samyn92/agent-tools/git-server:latest"
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

// ResolveGitWorkspace extracts git workspace configuration from an AgentResource.
func ResolveGitWorkspace(
	gitSpec *agentsv1alpha1.AgentRunGitSpec,
	resource *agentsv1alpha1.AgentResource,
) (*GitWorkspaceConfig, error) {
	cfg := &GitWorkspaceConfig{
		Branch:      gitSpec.Branch,
		BaseBranch:  gitSpec.BaseBranch,
		Credentials: resource.Spec.Credentials,
	}

	switch resource.Spec.Kind {
	case agentsv1alpha1.AgentResourceKindGitHubRepo:
		if resource.Spec.GitHub == nil {
			return nil, fmt.Errorf("AgentResource %q kind is github-repo but spec.github is nil", resource.Name)
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

	case agentsv1alpha1.AgentResourceKindGitLabProject:
		if resource.Spec.GitLab == nil {
			return nil, fmt.Errorf("AgentResource %q kind is gitlab-project but spec.gitlab is nil", resource.Name)
		}
		gl := resource.Spec.GitLab
		cfg.Provider = ProviderGitLab
		cfg.CloneURL = fmt.Sprintf("%s/%s.git", gl.BaseURL, gl.Project)
		cfg.GitLabProject = gl.Project
		cfg.GitLabBaseURL = gl.BaseURL
		if cfg.BaseBranch == "" {
			cfg.BaseBranch = gl.DefaultBranch
		}

	case agentsv1alpha1.AgentResourceKindGitRepo:
		if resource.Spec.Git == nil {
			return nil, fmt.Errorf("AgentResource %q kind is git-repo but spec.git is nil", resource.Name)
		}
		cfg.Provider = ProviderGit
		cfg.CloneURL = resource.Spec.Git.URL
		if cfg.BaseBranch == "" {
			cfg.BaseBranch = resource.Spec.Git.Branch
		}

	default:
		return nil, fmt.Errorf("AgentResource %q kind %q is not a git resource", resource.Name, resource.Spec.Kind)
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

// GitToolSidecars returns MCP gateway sidecar containers for the git tools.
// The git MCP tool is always included. GitHub or GitLab tool is added based on provider.
// startIndex is the MCP gateway port index offset (9001 + startIndex).
func (g *GitWorkspaceConfig) GitToolSidecars(startIndex int) []corev1.Container {
	var sidecars []corev1.Container

	// Git MCP tool (always needed for clone, commit, push, etc.)
	sidecars = append(sidecars, buildGitMCPSidecar("git", GitToolImage, startIndex, nil))

	// Platform-specific MCP tool (for PR/MR creation)
	switch g.Provider {
	case ProviderGitHub:
		extra := []corev1.EnvVar{
			{Name: "GH_TOKEN", ValueFrom: g.tokenEnvVarSource()},
		}
		if g.GitHubAPIURL != "" {
			extra = append(extra, corev1.EnvVar{Name: "GITHUB_API_URL", Value: g.GitHubAPIURL})
		}
		sidecars = append(sidecars, buildGitMCPSidecar("github", GitHubToolImage, startIndex+1, extra))

	case ProviderGitLab:
		extra := []corev1.EnvVar{
			{Name: "GITLAB_TOKEN", ValueFrom: g.tokenEnvVarSource()},
			{Name: "GITLAB_URL", Value: g.GitLabBaseURL},
		}
		sidecars = append(sidecars, buildGitMCPSidecar("gitlab", GitLabToolImage, startIndex+1, extra))
	}

	return sidecars
}

func (g *GitWorkspaceConfig) tokenEnvVarSource() *corev1.EnvVarSource {
	if g.Credentials == nil {
		return nil
	}
	return &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: g.Credentials.Name},
			Key:                  g.Credentials.Key,
		},
	}
}

// buildGitMCPSidecar creates a sidecar container running an MCP tool server
// via the mcp-gateway spawn mode. The tool server binary is already in the image.
func buildGitMCPSidecar(name, image string, index int, extraEnv []corev1.EnvVar) corev1.Container {
	port := int32(GatewayBasePort + index)

	env := make([]corev1.EnvVar, 0, 4+len(extraEnv))
	env = append(env,
		corev1.EnvVar{Name: "GATEWAY_MODE", Value: "spawn"},
		corev1.EnvVar{Name: "GATEWAY_PORT", Value: fmt.Sprintf("%d", port)},
		corev1.EnvVar{Name: "GATEWAY_COMMAND", Value: fmt.Sprintf("mcp-%s", name)},
		corev1.EnvVar{Name: "WORKSPACE", Value: "/data/repo"},
	)
	env = append(env, extraEnv...)

	gatewayVolume := fmt.Sprintf("gw-bin-%s", name)

	return corev1.Container{
		Name:    fmt.Sprintf("gw-git-%s", name),
		Image:   image,
		Command: []string{"/gateway/mcp-gateway"},
		Env:     env,
		Ports: []corev1.ContainerPort{
			{
				Name:          fmt.Sprintf("gw-git-%d", index),
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: gatewayVolume, MountPath: "/gateway", ReadOnly: true},
			{Name: VolumeData, MountPath: MountData},
		},
	}
}

// GitToolVolumes returns volumes needed for the git MCP tool sidecars.
// Each sidecar needs an emptyDir for the gateway binary copy.
func (g *GitWorkspaceConfig) GitToolVolumes() []corev1.Volume {
	volumes := []corev1.Volume{
		{Name: "gw-bin-git", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	switch g.Provider {
	case ProviderGitHub:
		volumes = append(volumes, corev1.Volume{
			Name: "gw-bin-github", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	case ProviderGitLab:
		volumes = append(volumes, corev1.Volume{
			Name: "gw-bin-gitlab", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}
	return volumes
}

// GitToolInitContainers returns init containers that copy the mcp-gateway binary
// into the shared emptyDir volumes for the sidecars.
func (g *GitWorkspaceConfig) GitToolInitContainers() []corev1.Container {
	inits := []corev1.Container{
		buildGatewayInitContainer("copy-gw-git", "gw-bin-git"),
	}
	switch g.Provider {
	case ProviderGitHub:
		inits = append(inits, buildGatewayInitContainer("copy-gw-github", "gw-bin-github"))
	case ProviderGitLab:
		inits = append(inits, buildGatewayInitContainer("copy-gw-gitlab", "gw-bin-gitlab"))
	}
	return inits
}

func buildGatewayInitContainer(name, volumeName string) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   MCPGatewayImage,
		Command: []string{"/mcp-gateway"},
		Args:    []string{"--copy-to=/gateway/mcp-gateway"},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeName, MountPath: "/gateway"},
		},
	}
}

// GitMCPServers returns MCPEntry items for the runtime config so it knows
// how to connect to the git MCP gateway sidecars.
// startIndex must match the startIndex used in GitToolSidecars.
func (g *GitWorkspaceConfig) GitMCPServers(startIndex int) []MCPEntry {
	entries := []MCPEntry{
		{
			Name:        "git",
			Port:        GatewayBasePort + startIndex,
			Description: "Git operations — status, diff, log, add, commit, push, pull, branch, clone",
			Category:    "git",
			UIHint:      "git",
		},
	}

	switch g.Provider {
	case ProviderGitHub:
		entries = append(entries, MCPEntry{
			Name:        "github",
			Port:        GatewayBasePort + startIndex + 1,
			Description: "GitHub API — PRs, issues, branches, checks, workflows",
			Category:    "git",
			UIHint:      "github",
		})
	case ProviderGitLab:
		entries = append(entries, MCPEntry{
			Name:        "gitlab",
			Port:        GatewayBasePort + startIndex + 1,
			Description: "GitLab API — MRs, issues, pipelines, projects",
			Category:    "git",
			UIHint:      "gitlab",
		})
	}

	return entries
}
