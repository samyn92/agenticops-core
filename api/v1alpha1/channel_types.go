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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChannelType defines the type of channel.
// +kubebuilder:validation:Enum=telegram;slack;discord;gitlab;github;webhook
type ChannelType string

const (
	ChannelTypeTelegram ChannelType = "telegram"
	ChannelTypeSlack    ChannelType = "slack"
	ChannelTypeDiscord  ChannelType = "discord"
	ChannelTypeGitLab   ChannelType = "gitlab"
	ChannelTypeGitHub   ChannelType = "github"
	ChannelTypeWebhook  ChannelType = "webhook"
)

// ChannelPhase describes the current phase of a Channel.
type ChannelPhase string

const (
	ChannelPhasePending ChannelPhase = "Pending"
	ChannelPhaseReady   ChannelPhase = "Ready"
	ChannelPhaseFailed  ChannelPhase = "Failed"
)

// IsEventType returns true if this channel type is event-driven (webhook/forge).
func (t ChannelType) IsEventType() bool {
	return t == ChannelTypeGitLab || t == ChannelTypeGitHub || t == ChannelTypeWebhook
}

// ChannelSpec defines the desired state of Channel.
type ChannelSpec struct {

	// ====================================================================
	// TYPE & TARGET
	// ====================================================================

	// Channel type: telegram, slack, discord, gitlab, github, webhook.
	// +kubebuilder:validation:Required
	Type ChannelType `json:"type"`

	// Name of the Agent CR to target.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// ====================================================================
	// PLATFORM CONFIG (exactly one, matching type)
	// ====================================================================

	// Telegram bot configuration.
	// +optional
	Telegram *TelegramChannelConfig `json:"telegram,omitempty"`

	// Slack bot configuration.
	// +optional
	Slack *SlackChannelConfig `json:"slack,omitempty"`

	// Discord bot configuration.
	// +optional
	Discord *DiscordChannelConfig `json:"discord,omitempty"`

	// GitLab webhook configuration.
	// +optional
	GitLab *GitLabChannelConfig `json:"gitlab,omitempty"`

	// GitHub webhook configuration.
	// +optional
	GitHub *GitHubChannelConfig `json:"github,omitempty"`

	// Generic webhook configuration.
	// +optional
	WebhookConfig *WebhookChannelConfig `json:"webhookConfig,omitempty"`

	// ====================================================================
	// PROMPT TEMPLATE (event-driven types only)
	// ====================================================================

	// Go text/template rendered with event data. Required for event types.
	// Chat types forward user messages directly.
	// +optional
	Prompt string `json:"prompt,omitempty"`

	// ====================================================================
	// INGRESS
	// ====================================================================

	// Webhook ingress configuration (host, TLS, etc.).
	// +optional
	Webhook *WebhookIngressConfig `json:"webhook,omitempty"`

	// ====================================================================
	// INFRASTRUCTURE
	// ====================================================================

	// Container image for the channel bridge.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Image pull policy.
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Number of replicas for the channel bridge.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Compute resources for the channel container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Security overrides for the channel pod. See SecurityOverrides.
	// +optional
	Security *SecurityOverrides `json:"security,omitempty"`
}

// ChannelStatus defines the observed state of Channel.
type ChannelStatus struct {
	// Current phase: Pending, Ready, Failed.
	// +optional
	Phase ChannelPhase `json:"phase,omitempty"`

	// Internal service URL.
	// +optional
	ServiceURL string `json:"serviceURL,omitempty"`

	// External webhook URL (if ingress configured).
	// +optional
	WebhookURL string `json:"webhookURL,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Channel.
const (
	ChannelConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ch
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Webhook",type=string,JSONPath=`.status.webhookURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Channel is the Schema for the channels API.
// Universal external ingress. Bridges external platforms to Agents.
type Channel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ChannelSpec   `json:"spec,omitempty"`
	Status ChannelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ChannelList contains a list of Channel.
type ChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Channel `json:"items"`
}
