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

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildChannelDeployment creates a Deployment for a Channel bridge.
func BuildChannelDeployment(ch *agentsv1alpha1.Channel, agent *agentsv1alpha1.Agent) *appsv1.Deployment {
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

	// Platform-specific env vars
	env = append(env, buildChannelPlatformEnv(ch)...)

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
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}
}

func buildChannelPlatformEnv(ch *agentsv1alpha1.Channel) []corev1.EnvVar {
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
	}

	return env
}

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
		// cert-manager annotation
		if ingress.Annotations == nil {
			ingress.Annotations = make(map[string]string)
		}
		ingress.Annotations["cert-manager.io/cluster-issuer"] = webhook.TLS.ClusterIssuer
	}

	return ingress
}
