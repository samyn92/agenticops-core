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
	"strings"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildChannelDeployment creates a Deployment for a Channel bridge.
// integration is the resolved Integration referenced by a poll-based channel
// (gitlab-label); it is nil for webhook/chat channel types.
func BuildChannelDeployment(ch *agentsv1alpha1.Channel, agent *agentsv1alpha1.Agent, integration *agentsv1alpha1.Integration) *appsv1.Deployment {
	labels := map[string]string{
		LabelComponent: "channel",
		LabelManagedBy: ManagedByValue,
		"app":          ch.Name,
	}

	replicas := int32(1)
	if ch.Spec.Replicas != nil {
		replicas = *ch.Spec.Replicas
	}

	// Build env vars for the channel bridge
	var env []corev1.EnvVar

	// Channel type
	env = append(env, corev1.EnvVar{Name: "CHANNEL_TYPE", Value: string(ch.Spec.Type)})

	// Target agent service URL (for daemon agents)
	if agent.Spec.Mode == agentsv1alpha1.AgentModeDaemon {
		env = append(env, corev1.EnvVar{
			Name:  "AGENT_URL",
			Value: AgentServiceURL(agent),
		})
	}

	// Agent ref (for creating AgentRuns)
	env = append(env, corev1.EnvVar{Name: "AGENT_REF", Value: ch.Spec.AgentRef})
	env = append(env, corev1.EnvVar{Name: "CHANNEL_NAME", Value: ch.Name})
	env = append(env, corev1.EnvVar{Name: "AGENT_MODE", Value: string(agent.Spec.Mode)})

	// Prompt template for event-driven channels
	if ch.Spec.Prompt != "" {
		env = append(env, corev1.EnvVar{Name: "PROMPT_TEMPLATE", Value: ch.Spec.Prompt})
	}

	// Inject POD_NAMESPACE from downward API (needed for AgentRun creation)
	env = append(env, corev1.EnvVar{
		Name: "POD_NAMESPACE",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.namespace",
			},
		},
	})

	// Platform-specific env vars
	env = append(env, buildChannelPlatformEnv(ch, integration)...)

	container := corev1.Container{
		Name:            "channel",
		Image:           ch.Spec.Image,
		ImagePullPolicy: ch.Spec.ImagePullPolicy,
		Env:             env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: 8080,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt32(8080),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    30,
			TimeoutSeconds:   1,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		},
	}

	if ch.Spec.Resources != nil {
		container.Resources = *ch.Spec.Resources
	}
	ensureEphemeralStorage(&container.Resources)

	podSpec := corev1.PodSpec{
		Containers:         []corev1.Container{container},
		ServiceAccountName: ChannelServiceAccountName(ch),
	}

	// Channel bridges in task mode need the SA token to create AgentRuns.
	// Ensure automount is enabled even if security overrides are nil.
	security := ch.Spec.Security
	if agent.Spec.Mode == agentsv1alpha1.AgentModeTask {
		if security == nil {
			security = &agentsv1alpha1.SecurityOverrides{}
		}
		if security.AutomountServiceAccountToken == nil {
			t := true
			security.AutomountServiceAccountToken = &t
		}
	}
	ApplySecurity(&podSpec, "channel", security)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ch.Name,
			Namespace: ch.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": ch.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}

func buildChannelPlatformEnv(ch *agentsv1alpha1.Channel, integration *agentsv1alpha1.Integration) []corev1.EnvVar {
	var env []corev1.EnvVar

	switch ch.Spec.Type {
	case agentsv1alpha1.ChannelTypeTelegram:
		if ch.Spec.Telegram != nil {
			env = append(env, corev1.EnvVar{
				Name: "TELEGRAM_BOT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.Telegram.BotTokenSecret.Name,
						},
						Key: ch.Spec.Telegram.BotTokenSecret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeSlack:
		if ch.Spec.Slack != nil {
			env = append(env, corev1.EnvVar{
				Name: "SLACK_BOT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.Slack.BotTokenSecret.Name,
						},
						Key: ch.Spec.Slack.BotTokenSecret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeDiscord:
		if ch.Spec.Discord != nil {
			env = append(env, corev1.EnvVar{
				Name: "DISCORD_BOT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.Discord.BotTokenSecret.Name,
						},
						Key: ch.Spec.Discord.BotTokenSecret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeGitLab:
		if ch.Spec.GitLab != nil {
			env = append(env, corev1.EnvVar{
				Name: "WEBHOOK_SECRET",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.GitLab.Secret.Name,
						},
						Key: ch.Spec.GitLab.Secret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeGitHub:
		if ch.Spec.GitHub != nil {
			env = append(env, corev1.EnvVar{
				Name: "WEBHOOK_SECRET",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.GitHub.Secret.Name,
						},
						Key: ch.Spec.GitHub.Secret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeWebhook:
		if ch.Spec.WebhookConfig != nil && ch.Spec.WebhookConfig.Secret != nil {
			env = append(env, corev1.EnvVar{
				Name: "WEBHOOK_SECRET",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ch.Spec.WebhookConfig.Secret.Name,
						},
						Key: ch.Spec.WebhookConfig.Secret.Key,
					},
				},
			})
		}

	case agentsv1alpha1.ChannelTypeGitLabLabel:
		env = append(env, buildGitLabLabelEnv(ch, integration)...)
	}

	return env
}

// buildGitLabLabelEnv emits the poll-config + GitLab identity env for a
// gitlab-label channel. The token is injected via SecretKeyRef from the bound
// Integration's credentials — the operator never reads the secret value.
func buildGitLabLabelEnv(ch *agentsv1alpha1.Channel, integration *agentsv1alpha1.Integration) []corev1.EnvVar {
	cfg := ch.Spec.GitLabLabel
	if cfg == nil || integration == nil {
		return nil
	}

	var env []corev1.EnvVar

	// GitLab identity from the bound Integration (project or group).
	switch {
	case integration.Spec.GitLab != nil:
		env = append(env,
			corev1.EnvVar{Name: "GITLAB_BASE_URL", Value: integration.Spec.GitLab.BaseURL},
			corev1.EnvVar{Name: "GITLAB_PROJECT", Value: integration.Spec.GitLab.Project},
		)
	case integration.Spec.GitLabGroup != nil:
		env = append(env,
			corev1.EnvVar{Name: "GITLAB_BASE_URL", Value: integration.Spec.GitLabGroup.BaseURL},
			corev1.EnvVar{Name: "GITLAB_GROUP", Value: integration.Spec.GitLabGroup.Group},
		)
	}

	// API token via SecretKeyRef (never read by the operator).
	if integration.Spec.Credentials != nil {
		env = append(env, corev1.EnvVar{
			Name: "GITLAB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: integration.Spec.Credentials.Name},
					Key:                  integration.Spec.Credentials.Key,
				},
			},
		})
	}

	// Poll configuration.
	target := cfg.Target
	if target == "" {
		target = "issues"
	}
	state := cfg.State
	if state == "" {
		state = "opened"
	}
	interval := "30s"
	if cfg.PollInterval != nil && cfg.PollInterval.Duration > 0 {
		interval = cfg.PollInterval.Duration.String()
	}
	env = append(env,
		corev1.EnvVar{Name: "GITLAB_TARGET", Value: target},
		corev1.EnvVar{Name: "GITLAB_LABELS", Value: strings.Join(cfg.Labels, ",")},
		corev1.EnvVar{Name: "GITLAB_STATE", Value: state},
		corev1.EnvVar{Name: "GITLAB_POLL_INTERVAL", Value: interval},
	)

	// Task-mode work-board runs get their GitLab identity (clone token +
	// native gitlab_* tools) from AgentRun.spec.git, which the bridge builds
	// from these. The integration name becomes spec.git.integrationRef; the
	// base branch is the integration's default branch (fallback "main").
	baseBranch := "main"
	if integration.Spec.GitLab != nil && integration.Spec.GitLab.DefaultBranch != "" {
		baseBranch = integration.Spec.GitLab.DefaultBranch
	}
	env = append(env,
		corev1.EnvVar{Name: "GITLAB_INTEGRATION_REF", Value: cfg.IntegrationRef},
		corev1.EnvVar{Name: "GITLAB_BASE_BRANCH", Value: baseBranch},
	)

	// Hybrid label protocol, agent half: when the channel does not override the
	// prompt, inject a default that drives the second transition
	// (in-progress -> needs-review). The bridge already performs the first,
	// deterministic transition (trigger -> in-progress) at fire time; the agent
	// is responsible for moving the card to review after it opens the MR.
	if ch.Spec.Prompt == "" {
		env = append(env, corev1.EnvVar{Name: "PROMPT_TEMPLATE", Value: defaultGitLabLabelPrompt})
	}

	return env
}

// defaultGitLabLabelPrompt is the work-board prompt rendered by the gitlab-label
// bridge when a Channel does not set spec.prompt. It is a Go text/template
// fed the poller's event data (.gitlab.iid/.title/.project/.web_url/.target/.label).
const defaultGitLabLabelPrompt = `You are an autonomous coding agent working a GitLab work board for project {{.gitlab.project}}.

A {{.gitlab.target}} item needs your attention:
- #{{.gitlab.iid}}: {{.gitlab.title}}
- URL: {{.gitlab.web_url}}
- Trigger: {{.gitlab.label}}

The board has already moved this item to ` + "`agent::in-progress`" + `. Your repository is cloned and checked out on your feature branch.
{{if eq .gitlab.target "merge_requests"}}
The work-board CARD is merge request !{{.gitlab.iid}}. You will review/iterate on it.

DEFINITION OF DONE — you have NOT finished until the card (merge request !{{.gitlab.iid}}) has been moved to ` + "`agent::needs-review`" + `. Leaving a comment or saving to memory does NOT complete the task. Your VERY LAST tool call MUST be gitlab_update_mr on iid {{.gitlab.iid}} that adds ` + "`agent::needs-review`" + ` and removes ` + "`agent::in-progress`" + `. Do not end your turn before doing this.

Steps:
1. Read the merge request with gitlab_get_mr and gitlab_get_mr_diff to understand it.
2. Make the requested changes, commit, and push the branch.
3. REQUIRED FINAL ACTION: gitlab_update_mr with iid {{.gitlab.iid}}, add_labels "agent::needs-review", remove_labels "agent::in-progress".
{{else}}
The work-board CARD is issue #{{.gitlab.iid}} — NOT any merge request. The merge request you open is the deliverable; do NOT relabel the merge request. The label that drives this board lives on the ISSUE.

DEFINITION OF DONE — you have NOT finished until issue #{{.gitlab.iid}} has been moved to ` + "`agent::needs-review`" + `. Opening the merge request, commenting, relabeling the merge request, or saving to memory does NOT complete the task. Your VERY LAST tool call MUST be gitlab_update_issue on iid {{.gitlab.iid}} that adds ` + "`agent::needs-review`" + ` and removes ` + "`agent::in-progress`" + `. Do not end your turn before doing this.
{{if eq .gitlab.label "agent::changes-requested"}}
THIS IS A REWORK. A merge request already exists for this issue and a human has requested changes. Do NOT open a new merge request (gitlab_create_mr will fail — one already exists for your branch).

Steps:
1. Find the open merge request for this issue: gitlab_list_mrs and match the one whose source branch is your feature branch.
2. Read the reviewer feedback: gitlab_list_mr_notes on that merge request IID, plus gitlab_get_mr_diff to see current state. Address every requested change.
3. Implement the requested changes, commit, and push to the SAME existing feature branch (the merge request updates automatically). Do NOT create a new branch or a new merge request.
4. REQUIRED FINAL ACTION: gitlab_update_issue with iid {{.gitlab.iid}}, add_labels "agent::needs-review", remove_labels "agent::in-progress".
{{else}}
Steps:
1. Read the full issue with gitlab_get_issue to understand exactly what is requested.
2. Explore the repository and implement the change.
3. Commit your work and push the feature branch.
4. Open a merge request targeting the default branch with gitlab_create_mr. Reference issue #{{.gitlab.iid}} in the description. Do NOT add work-board labels to this merge request.
5. REQUIRED FINAL ACTION: gitlab_update_issue with iid {{.gitlab.iid}}, add_labels "agent::needs-review", remove_labels "agent::in-progress".
{{end}}
{{end}}
Do NOT merge the merge request — a human reviews and merges. If you genuinely cannot complete the task, your required final action is the same {{if eq .gitlab.target "merge_requests"}}gitlab_update_mr{{else}}gitlab_update_issue{{end}} call but adding ` + "`agent::changes-requested`" + ` instead of ` + "`agent::needs-review`" + ` (and removing ` + "`agent::in-progress`" + `), after explaining why with a note. Either way, your last tool call always moves card #{{.gitlab.iid}} off agent::in-progress.`

// BuildChannelIngress creates an Ingress for a Channel's webhook endpoint.
func BuildChannelIngress(ch *agentsv1alpha1.Channel) *networkingv1.Ingress {
	if ch.Spec.Webhook == nil {
		return nil
	}

	webhook := ch.Spec.Webhook
	path := webhook.Path
	if path == "" {
		path = "/"
	}

	pathType := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ch.Name,
			Namespace: ch.Namespace,
			Labels: map[string]string{
				LabelComponent: "channel",
				LabelManagedBy: ManagedByValue,
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: webhook.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: ch.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Ingress class
	if webhook.IngressClassName != "" {
		ingress.Spec.IngressClassName = &webhook.IngressClassName
	}

	// TLS
	if webhook.TLS != nil {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{webhook.Host},
				SecretName: fmt.Sprintf("%s-tls", ch.Name),
			},
		}
		if ingress.Annotations == nil {
			ingress.Annotations = make(map[string]string)
		}
		// cert-manager annotation: prefer namespaced Issuer, fall back to ClusterIssuer.
		if webhook.TLS.Issuer != "" {
			ingress.Annotations["cert-manager.io/issuer"] = webhook.TLS.Issuer
		} else if webhook.TLS.ClusterIssuer != "" {
			ingress.Annotations["cert-manager.io/cluster-issuer"] = webhook.TLS.ClusterIssuer
		}
	}

	return ingress
}
