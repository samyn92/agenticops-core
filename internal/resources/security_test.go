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
	"strings"
	"testing"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	psaapi "k8s.io/pod-security-admission/api"
	psapolicy "k8s.io/pod-security-admission/policy"
)

// restrictedEvaluator returns the canonical upstream PSA evaluator pinned
// to the "restricted" profile at the latest policy version. Test pod specs
// rendered by every operator builder must satisfy it without exception.
func restrictedEvaluator(t *testing.T) psapolicy.Evaluator {
	t.Helper()
	ev, err := psapolicy.NewEvaluator(psapolicy.DefaultChecks(), nil)
	if err != nil {
		t.Fatalf("psapolicy.NewEvaluator: %v", err)
	}
	return ev
}

// assertRestrictedCompliant runs the upstream PSA "restricted" checks
// against the pod spec and fails the test with a precise list of violated
// checks if any are reported.
func assertRestrictedCompliant(t *testing.T, label string, meta *metav1.ObjectMeta, spec *corev1.PodSpec) {
	t.Helper()
	ev := restrictedEvaluator(t)
	lv := psaapi.LevelVersion{Level: psaapi.LevelRestricted, Version: psaapi.LatestVersion()}
	results := ev.EvaluatePod(lv, meta, spec)
	var failures []string
	for _, r := range results {
		if !r.Allowed {
			failures = append(failures, r.ForbiddenReason+": "+r.ForbiddenDetail)
		}
	}
	if len(failures) > 0 {
		t.Errorf("%s: pod spec violates restricted PSS:\n  - %s",
			label, strings.Join(failures, "\n  - "))
	}
}

// ----------------------------------------------------------------------
// Builder-output compliance — every operator-built pod must clear the
// upstream restricted Pod Security Standard out of the box, with no
// per-CR overrides.
// ----------------------------------------------------------------------

func TestRestrictedPSS_AgentDeployment(t *testing.T) {
	d := BuildAgentDeployment(testAgent(), nil, nil)
	assertRestrictedCompliant(t, "Agent Deployment",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestRestrictedPSS_AgentDeployment_WithMCPTools(t *testing.T) {
	agent := testAgent()
	agent.Spec.Tools = []agentsv1alpha1.AgentToolBinding{{Name: "kubectl"}}
	tools := []agentsv1alpha1.AgentTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "kubectl", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentToolSpec{
			MCPEndpoint: &agentsv1alpha1.MCPEndpointToolSource{
				URL: "http://mcp-kubectl:8080",
			},
		},
	}}
	d := BuildAgentDeployment(agent, tools, nil)
	assertRestrictedCompliant(t, "Agent Deployment with MCP gateway sidecar",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestRestrictedPSS_AgentDeployment_WithOAuth2Provider(t *testing.T) {
	agent := testAgent()
	providers := []agentsv1alpha1.Provider{{
		ObjectMeta: metav1.ObjectMeta{Name: "dnabot", Namespace: "agents"},
		Spec: agentsv1alpha1.ProviderSpec{
			Endpoint: &agentsv1alpha1.ProviderEndpoint{
				BaseURL: "https://example.com/v1",
				OAuth2ClientCredentials: &agentsv1alpha1.OAuth2ClientCredentials{
					TokenURL:           "https://example.com/oauth/token",
					ClientIDSecret:     agentsv1alpha1.SecretKeyRef{Name: "s", Key: "id"},
					ClientSecretSecret: agentsv1alpha1.SecretKeyRef{Name: "s", Key: "secret"},
				},
			},
		},
	}}
	d := BuildAgentDeployment(agent, nil, providers)
	assertRestrictedCompliant(t, "Agent Deployment with token-injector sidecar",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestRestrictedPSS_AgentToolDeployment(t *testing.T) {
	tool := &agentsv1alpha1.AgentTool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-mcp", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentToolSpec{
			MCPServer: &agentsv1alpha1.MCPServerToolSource{
				Image: "ghcr.io/example/echo-mcp:1.0",
			},
		},
	}
	d := BuildAgentToolDeployment(tool)
	assertRestrictedCompliant(t, "AgentTool MCP server Deployment",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestRestrictedPSS_ChannelDeployment(t *testing.T) {
	agent := testAgent()
	ch := &agentsv1alpha1.Channel{
		ObjectMeta: metav1.ObjectMeta{Name: "gitlab-bridge", Namespace: "agents"},
		Spec: agentsv1alpha1.ChannelSpec{
			Type:     agentsv1alpha1.ChannelTypeGitLab,
			AgentRef: agent.Name,
			Image:    "ghcr.io/samyn92/agent-channels/gitlab:1.0",
			GitLab: &agentsv1alpha1.GitLabChannelConfig{
				Secret: agentsv1alpha1.SecretKeyRef{Name: "gl", Key: "token"},
			},
		},
	}
	d := BuildChannelDeployment(ch, agent)
	assertRestrictedCompliant(t, "Channel bridge Deployment",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

// ----------------------------------------------------------------------
// Safety-floor merge — user-supplied overrides cannot weaken the floor.
// ----------------------------------------------------------------------

func TestComputeSecurityViolations_Empty(t *testing.T) {
	if v := ComputeSecurityViolations(nil); len(v) != 0 {
		t.Errorf("expected no violations for nil overrides, got %v", v)
	}
	if v := ComputeSecurityViolations(&agentsv1alpha1.SecurityOverrides{}); len(v) != 0 {
		t.Errorf("expected no violations for empty overrides, got %v", v)
	}
}

func TestComputeSecurityViolations_RejectsWeakerPodFields(t *testing.T) {
	f := false
	zero := int64(0)
	overrides := &agentsv1alpha1.SecurityOverrides{
		Pod: &corev1.PodSecurityContext{
			RunAsNonRoot: &f,
			RunAsUser:    &zero,
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeUnconfined,
			},
		},
	}
	v := ComputeSecurityViolations(overrides)
	expectContains(t, v, "pod.runAsNonRoot=false")
	expectContains(t, v, "pod.runAsUser=0")
	expectContains(t, v, "Unconfined")
}

func TestComputeSecurityViolations_RejectsWeakerContainerFields(t *testing.T) {
	tr := true
	f := false
	zero := int64(0)
	overrides := &agentsv1alpha1.SecurityOverrides{
		Container: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &tr,
			Privileged:               &tr,
			ReadOnlyRootFilesystem:   &f,
			RunAsNonRoot:             &f,
			RunAsUser:                &zero,
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{"NET_ADMIN"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeUnconfined,
			},
		},
	}
	v := ComputeSecurityViolations(overrides)
	expectContains(t, v, "allowPrivilegeEscalation=true")
	expectContains(t, v, "privileged=true")
	expectContains(t, v, "readOnlyRootFilesystem=false")
	expectContains(t, v, "runAsNonRoot=false")
	expectContains(t, v, "runAsUser=0")
	expectContains(t, v, "capabilities.add")
	expectContains(t, v, "Unconfined")
}

func TestComputeSecurityViolations_AcceptsBenignOverrides(t *testing.T) {
	uid := int64(2000)
	gid := int64(2000)
	overrides := &agentsv1alpha1.SecurityOverrides{
		Pod: &corev1.PodSecurityContext{
			RunAsUser:  &uid,
			RunAsGroup: &gid,
			FSGroup:    &gid,
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Container: &corev1.SecurityContext{
			RunAsUser: &uid,
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
	}
	if v := ComputeSecurityViolations(overrides); len(v) != 0 {
		t.Errorf("expected no violations for benign overrides, got %v", v)
	}
}

func TestApplySecurity_RestrictedFloorAlwaysHolds_EvenWithMaliciousOverrides(t *testing.T) {
	tr := true
	f := false
	zero := int64(0)
	agent := testAgent()
	agent.Spec.Security = &agentsv1alpha1.SecurityOverrides{
		Pod: &corev1.PodSecurityContext{
			RunAsNonRoot: &f,
			RunAsUser:    &zero,
		},
		Container: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &tr,
			Privileged:               &tr,
			ReadOnlyRootFilesystem:   &f,
			RunAsNonRoot:             &f,
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{"NET_ADMIN", "SYS_ADMIN"},
			},
		},
	}
	d := BuildAgentDeployment(agent, nil, nil)
	assertRestrictedCompliant(t, "Agent Deployment with malicious overrides",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestApplySecurity_BenignOverridesArePreserved(t *testing.T) {
	uid := int64(2000)
	agent := testAgent()
	agent.Spec.Security = &agentsv1alpha1.SecurityOverrides{
		Pod: &corev1.PodSecurityContext{
			RunAsUser: &uid,
		},
	}
	d := BuildAgentDeployment(agent, nil, nil)
	got := d.Spec.Template.Spec.SecurityContext
	if got == nil || got.RunAsUser == nil || *got.RunAsUser != uid {
		t.Fatalf("expected pod.runAsUser=%d, got %+v", uid, got)
	}
	assertRestrictedCompliant(t, "Agent Deployment with benign override",
		&d.Spec.Template.ObjectMeta, &d.Spec.Template.Spec)
}

func TestApplySecurity_AutomountServiceAccountTokenDefaultsFalse(t *testing.T) {
	d := BuildAgentDeployment(testAgent(), nil, nil)
	a := d.Spec.Template.Spec.AutomountServiceAccountToken
	if a == nil || *a {
		t.Fatalf("expected automountServiceAccountToken=false, got %+v", a)
	}
}

func TestApplySecurity_AutomountServiceAccountTokenOptIn(t *testing.T) {
	tr := true
	agent := testAgent()
	agent.Spec.Security = &agentsv1alpha1.SecurityOverrides{
		AutomountServiceAccountToken: &tr,
	}
	d := BuildAgentDeployment(agent, nil, nil)
	a := d.Spec.Template.Spec.AutomountServiceAccountToken
	if a == nil || !*a {
		t.Fatalf("expected automountServiceAccountToken=true, got %+v", a)
	}
}

// expectContains fails the test if no element of haystack contains needle.
func expectContains(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return
		}
	}
	t.Errorf("expected violations to include %q, got %v", needle, haystack)
}
