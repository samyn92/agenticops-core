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
	"testing"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func envMap(env []corev1.EnvVar) map[string]corev1.EnvVar {
	m := make(map[string]corev1.EnvVar, len(env))
	for _, e := range env {
		m[e.Name] = e
	}
	return m
}

func glProject(name, base, project string) agentsv1alpha1.Integration {
	return agentsv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"},
		Spec: agentsv1alpha1.IntegrationSpec{
			Kind:        agentsv1alpha1.IntegrationKindGitLabProject,
			DisplayName: name,
			Credentials: &agentsv1alpha1.SecretKeyRef{Name: "gl-secret", Key: "token"},
			GitLab:      &agentsv1alpha1.GitLabResourceConfig{BaseURL: base, Project: project},
		},
	}
}

func glGroup(name, base, group string, projects []string, readOnly bool) agentsv1alpha1.Integration {
	return agentsv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"},
		Spec: agentsv1alpha1.IntegrationSpec{
			Kind:        agentsv1alpha1.IntegrationKindGitLabGroup,
			DisplayName: name,
			Credentials: &agentsv1alpha1.SecretKeyRef{Name: "gl-secret", Key: "token"},
			GitLabGroup: &agentsv1alpha1.GitLabGroupResourceConfig{
				BaseURL:  base,
				Group:    group,
				Projects: projects,
				ReadOnly: readOnly,
			},
		},
	}
}

func TestGitLabEnvFromIntegrations_NoneBound(t *testing.T) {
	got := GitLabEnvFromIntegrations(nil, nil)
	if got != nil {
		t.Fatalf("expected nil for no integrations, got %v", got)
	}
	// A non-gitlab integration is ignored.
	gh := agentsv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "gh"},
		Spec:       agentsv1alpha1.IntegrationSpec{Kind: agentsv1alpha1.IntegrationKindGitHubRepo},
	}
	if got := GitLabEnvFromIntegrations([]agentsv1alpha1.Integration{gh}, nil); got != nil {
		t.Fatalf("expected nil for non-gitlab integration, got %v", got)
	}
}

func TestGitLabEnvFromIntegrations_Project(t *testing.T) {
	ints := []agentsv1alpha1.Integration{glProject("repo", "https://gitlab.com", "grp/sub/app")}
	bindings := map[string]agentsv1alpha1.IntegrationBinding{"repo": {Name: "repo"}}

	m := envMap(GitLabEnvFromIntegrations(ints, bindings))
	if m["GITLAB_URL"].Value != "https://gitlab.com" {
		t.Errorf("GITLAB_URL = %q", m["GITLAB_URL"].Value)
	}
	if m["GITLAB_PROJECT"].Value != "grp/sub/app" {
		t.Errorf("GITLAB_PROJECT = %q", m["GITLAB_PROJECT"].Value)
	}
	if _, ok := m["GITLAB_READONLY"]; ok {
		t.Error("GITLAB_READONLY should be unset when binding is not read-only")
	}
	tok := m["GITLAB_TOKEN"]
	if tok.ValueFrom == nil || tok.ValueFrom.SecretKeyRef == nil ||
		tok.ValueFrom.SecretKeyRef.Name != "gl-secret" || tok.ValueFrom.SecretKeyRef.Key != "token" {
		t.Errorf("GITLAB_TOKEN not wired from secret: %+v", tok)
	}
	if _, ok := m["GIT_TOKEN"]; !ok {
		t.Error("GIT_TOKEN (credential helper) should also be set")
	}
}

func TestGitLabEnvFromIntegrations_BindingReadOnly(t *testing.T) {
	ints := []agentsv1alpha1.Integration{glProject("repo", "https://gitlab.com", "grp/app")}
	bindings := map[string]agentsv1alpha1.IntegrationBinding{"repo": {Name: "repo", ReadOnly: true}}
	m := envMap(GitLabEnvFromIntegrations(ints, bindings))
	if m["GITLAB_READONLY"].Value != "true" {
		t.Errorf("expected GITLAB_READONLY=true from binding, got %q", m["GITLAB_READONLY"].Value)
	}
}

func TestGitLabEnvFromIntegrations_GroupPreferredAndAllowList(t *testing.T) {
	ints := []agentsv1alpha1.Integration{
		glProject("repo", "https://gitlab.com", "grp/app"),
		glGroup("grp", "https://gl.example.com", "grp", []string{"grp/app", "grp/infra"}, true),
	}
	bindings := map[string]agentsv1alpha1.IntegrationBinding{
		"repo": {Name: "repo"},
		"grp":  {Name: "grp"},
	}
	m := envMap(GitLabEnvFromIntegrations(ints, bindings))
	// Group wins over project.
	if m["GITLAB_GROUP"].Value != "grp" {
		t.Errorf("expected GITLAB_GROUP=grp, got %q", m["GITLAB_GROUP"].Value)
	}
	if _, ok := m["GITLAB_PROJECT"]; ok {
		t.Error("GITLAB_PROJECT should not be set when a group identity is chosen")
	}
	if m["GITLAB_URL"].Value != "https://gl.example.com" {
		t.Errorf("expected group baseURL, got %q", m["GITLAB_URL"].Value)
	}
	if m["GITLAB_PROJECTS"].Value != "grp/app,grp/infra" {
		t.Errorf("GITLAB_PROJECTS = %q", m["GITLAB_PROJECTS"].Value)
	}
	if m["GITLAB_READONLY"].Value != "true" {
		t.Errorf("expected GITLAB_READONLY=true from group.readOnly, got %q", m["GITLAB_READONLY"].Value)
	}
}
