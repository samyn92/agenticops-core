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
	"slices"
	"strings"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
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

	case agentsv1alpha1.IntegrationKindGitLabGroup:
		glg := resource.Spec.GitLabGroup
		if glg == nil {
			return nil, fmt.Errorf("integration %q kind is gitlab-group but spec.gitlabGroup is nil", resource.Name)
		}
		// A group binds many projects; the caller (e.g. a delegating manager)
		// must name the specific project inside the group to clone.
		if gitSpec.Project == "" {
			return nil, fmt.Errorf("integration %q is a gitlab-group; spec.git.project (full project path, e.g. %q/<repo>) is required", resource.Name, glg.Group)
		}
		// Safety: the group token must only ever clone projects inside its group.
		if !strings.HasPrefix(gitSpec.Project, glg.Group+"/") {
			return nil, fmt.Errorf("project %q is not within gitlab-group %q", gitSpec.Project, glg.Group)
		}
		// Honour an explicit allow-list when the integration declares one.
		if len(glg.Projects) > 0 && !slices.Contains(glg.Projects, gitSpec.Project) {
			return nil, fmt.Errorf("project %q is not in the allow-list for gitlab-group %q", gitSpec.Project, resource.Name)
		}
		cfg.Provider = ProviderGitLab
		cfg.CloneURL = fmt.Sprintf("%s/%s.git", glg.BaseURL, gitSpec.Project)
		cfg.GitLabProject = gitSpec.Project
		cfg.GitLabBaseURL = glg.BaseURL

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
			// Bind the runtime's native gitlab_* tools to the project being
			// worked on so label/MR calls default to the right project (the
			// tools read GITLAB_PROJECT). For a gitlab-group clone this is the
			// specific project path inside the group.
			corev1.EnvVar{Name: "GITLAB_PROJECT", Value: g.GitLabProject},
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

// GitLabEnvFromIntegrations exposes a single bound GitLab identity to the
// runtime's native gitlab_* tools via the env contract they expect
// (GITLAB_URL / GITLAB_TOKEN / GITLAB_GROUP / GITLAB_PROJECT /
// GITLAB_PROJECTS / GITLAB_READONLY).
//
// One agent carries one GitLab identity (the native tools read a single
// token). A bound gitlab-group is preferred over a gitlab-project; among
// equals the first in declaration order wins. Returns nil when no GitLab
// integration is bound. Credentials are injected as a SecretKeyRef (never
// rendered into config). Used only for daemon agents — task-mode runs get
// their git identity from AgentRun.spec.git via GitEnvVars.
func GitLabEnvFromIntegrations(
	integrations []agentsv1alpha1.Integration,
	bindings map[string]agentsv1alpha1.IntegrationBinding,
) []corev1.EnvVar {
	var chosen *agentsv1alpha1.Integration
	for i := range integrations {
		switch integrations[i].Spec.Kind {
		case agentsv1alpha1.IntegrationKindGitLabGroup:
			// Group wins outright.
			chosen = &integrations[i]
		case agentsv1alpha1.IntegrationKindGitLabProject:
			if chosen == nil {
				chosen = &integrations[i]
			}
		}
		if chosen != nil && chosen.Spec.Kind == agentsv1alpha1.IntegrationKindGitLabGroup {
			break
		}
	}
	if chosen == nil {
		return nil
	}

	binding := bindings[chosen.Name]
	readOnly := binding.ReadOnly

	var env []corev1.EnvVar
	switch chosen.Spec.Kind {
	case agentsv1alpha1.IntegrationKindGitLabProject:
		gl := chosen.Spec.GitLab
		if gl == nil {
			return nil
		}
		env = append(env,
			corev1.EnvVar{Name: "GITLAB_URL", Value: gl.BaseURL},
			corev1.EnvVar{Name: "GITLAB_PROJECT", Value: gl.Project},
		)
	case agentsv1alpha1.IntegrationKindGitLabGroup:
		gl := chosen.Spec.GitLabGroup
		if gl == nil {
			return nil
		}
		env = append(env,
			corev1.EnvVar{Name: "GITLAB_URL", Value: gl.BaseURL},
			corev1.EnvVar{Name: "GITLAB_GROUP", Value: gl.Group},
		)
		if len(gl.Projects) > 0 {
			env = append(env, corev1.EnvVar{Name: "GITLAB_PROJECTS", Value: strings.Join(gl.Projects, ",")})
		}
		if gl.ReadOnly {
			readOnly = true
		}
	}

	if readOnly {
		env = append(env, corev1.EnvVar{Name: "GITLAB_READONLY", Value: "true"})
	}

	// Token (and GIT_TOKEN for the git credential helper) from the Secret.
	if chosen.Spec.Credentials != nil {
		for _, name := range []string{"GITLAB_TOKEN", "GIT_TOKEN"} {
			env = append(env, corev1.EnvVar{
				Name: name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: chosen.Spec.Credentials.Name},
						Key:                  chosen.Spec.Credentials.Key,
					},
				},
			})
		}
	}

	return env
}

// GitToolEntries returns ToolEntry items for the runtime config so it discovers
// git platform tools via loadOCITools() (stdio exec).
// The tool binary must be declared in the agent's spec.tools[] as an OCI artifact.
// This method only provides the config.json entries — the init containers are
// generated from the agent's spec.tools[].
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
