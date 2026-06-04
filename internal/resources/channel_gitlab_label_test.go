package resources

import (
	"strings"
	"testing"
	"text/template"
	"time"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDefaultGitLabLabelPrompt_Renders ensures the default prompt template parses
// and executes for every target/trigger-label combination the bridge can feed it.
// Guards against unbalanced {{if}}/{{end}} blocks in the rework branch.
func TestDefaultGitLabLabelPrompt_Renders(t *testing.T) {
	tmpl, err := template.New("prompt").Parse(defaultGitLabLabelPrompt)
	if err != nil {
		t.Fatalf("default prompt must parse: %v", err)
	}
	cases := []struct{ target, label string }{
		{"issues", "agent::todo"},
		{"issues", "agent::changes-requested"},
		{"merge_requests", "agent::todo"},
	}
	for _, c := range cases {
		data := map[string]any{"gitlab": map[string]any{
			"target": c.target, "label": c.label, "iid": 11,
			"title": "t", "project": "p", "web_url": "u",
		}}
		var sb strings.Builder
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Errorf("execute target=%s label=%s: %v", c.target, c.label, err)
		}
	}
}

// gitlabLabelChannel returns a gitlab-label Channel bound to a project Integration.
func gitlabLabelChannel() *agentsv1alpha1.Channel {
	interval := metav1.Duration{Duration: 45 * time.Second}
	return &agentsv1alpha1.Channel{
		ObjectMeta: metav1.ObjectMeta{Name: "board", Namespace: "agents"},
		Spec: agentsv1alpha1.ChannelSpec{
			Type:     agentsv1alpha1.ChannelTypeGitLabLabel,
			AgentRef: "test-agent",
			Image:    "ghcr.io/samyn92/agent-channels/gitlab-label:1.0",
			GitLabLabel: &agentsv1alpha1.GitLabLabelChannelConfig{
				IntegrationRef: "homecluster-repo",
				Target:         "issues",
				Labels:         []string{"agent::todo", "agent::changes-requested"},
				State:          "opened",
				PollInterval:   &interval,
			},
		},
	}
}

func gitlabProjectIntegration() *agentsv1alpha1.Integration {
	return &agentsv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "homecluster-repo", Namespace: "agents"},
		Spec: agentsv1alpha1.IntegrationSpec{
			Kind:        agentsv1alpha1.IntegrationKindGitLabProject,
			Credentials: &agentsv1alpha1.SecretKeyRef{Name: "gitlab-token", Key: "token"},
			GitLab: &agentsv1alpha1.GitLabResourceConfig{
				BaseURL: "https://gitlab.com",
				Project: "samyn92/homecluster",
			},
		},
	}
}

func gitlabEnvMap(t *testing.T, ch *agentsv1alpha1.Channel, intg *agentsv1alpha1.Integration) (map[string]string, map[string]string) {
	t.Helper()
	d := BuildChannelDeployment(ch, testAgent(), intg)
	values := map[string]string{}
	secretRefs := map[string]string{}
	for _, c := range d.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				secretRefs[e.Name] = e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
				continue
			}
			values[e.Name] = e.Value
		}
	}
	return values, secretRefs
}

func TestBuildChannelDeployment_GitLabLabelEnv(t *testing.T) {
	values, secretRefs := gitlabEnvMap(t, gitlabLabelChannel(), gitlabProjectIntegration())

	wantVals := map[string]string{
		"GITLAB_BASE_URL":      "https://gitlab.com",
		"GITLAB_PROJECT":       "samyn92/homecluster",
		"GITLAB_TARGET":        "issues",
		"GITLAB_LABELS":        "agent::todo,agent::changes-requested",
		"GITLAB_STATE":         "opened",
		"GITLAB_POLL_INTERVAL": "45s",
		// Task-mode git workspace identity for AgentRun.spec.git.
		"GITLAB_INTEGRATION_REF": "homecluster-repo",
		"GITLAB_BASE_BRANCH":     "main",
	}
	for k, want := range wantVals {
		if got := values[k]; got != want {
			t.Errorf("env %s = %q, want %q", k, got, want)
		}
	}
	if _, ok := values["GITLAB_GROUP"]; ok {
		t.Errorf("GITLAB_GROUP must not be set for a project Integration")
	}

	// Token must be injected via SecretKeyRef, never as a literal value.
	if got := secretRefs["GITLAB_TOKEN"]; got != "gitlab-token/token" {
		t.Errorf("GITLAB_TOKEN secretRef = %q, want %q", got, "gitlab-token/token")
	}
	if _, ok := values["GITLAB_TOKEN"]; ok {
		t.Errorf("GITLAB_TOKEN must not be set as a literal value")
	}

	// With no spec.prompt set, the operator injects the default work-board
	// prompt that drives the agent half of the hybrid label protocol.
	if got := values["PROMPT_TEMPLATE"]; got == "" {
		t.Errorf("PROMPT_TEMPLATE must default for gitlab-label channels with no spec.prompt")
	} else if !strings.Contains(got, "agent::needs-review") || !strings.Contains(got, "gitlab_update_issue") {
		t.Errorf("default PROMPT_TEMPLATE must instruct the needs-review transition, got %q", got)
	} else if !strings.Contains(got, "agent::changes-requested") || !strings.Contains(got, "gitlab_list_mr_notes") {
		t.Errorf("default PROMPT_TEMPLATE must include the rework loop (read review notes on changes-requested), got %q", got)
	}
}

func TestBuildChannelDeployment_GitLabLabelDefaults(t *testing.T) {
	ch := gitlabLabelChannel()
	// Drop optionals to exercise operator-side defaults.
	ch.Spec.GitLabLabel.Target = ""
	ch.Spec.GitLabLabel.State = ""
	ch.Spec.GitLabLabel.PollInterval = nil

	values, _ := gitlabEnvMap(t, ch, gitlabProjectIntegration())
	wantDefaults := map[string]string{
		"GITLAB_TARGET":        "issues",
		"GITLAB_STATE":         "opened",
		"GITLAB_POLL_INTERVAL": "30s",
	}
	for k, want := range wantDefaults {
		if got := values[k]; got != want {
			t.Errorf("default env %s = %q, want %q", k, got, want)
		}
	}
}

func TestBuildChannelDeployment_GitLabLabelGroupIdentity(t *testing.T) {
	intg := gitlabProjectIntegration()
	intg.Spec.Kind = agentsv1alpha1.IntegrationKindGitLabGroup
	intg.Spec.GitLab = nil
	intg.Spec.GitLabGroup = &agentsv1alpha1.GitLabGroupResourceConfig{
		BaseURL: "https://gitlab.example.com",
		Group:   "myorg/platform",
	}

	values, _ := gitlabEnvMap(t, gitlabLabelChannel(), intg)
	if got := values["GITLAB_GROUP"]; got != "myorg/platform" {
		t.Errorf("GITLAB_GROUP = %q, want %q", got, "myorg/platform")
	}
	if got := values["GITLAB_BASE_URL"]; got != "https://gitlab.example.com" {
		t.Errorf("GITLAB_BASE_URL = %q, want %q", got, "https://gitlab.example.com")
	}
	if _, ok := values["GITLAB_PROJECT"]; ok {
		t.Errorf("GITLAB_PROJECT must not be set for a group Integration")
	}
}
