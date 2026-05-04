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

// ProviderType selects the Fantasy SDK backend.
// +kubebuilder:validation:Enum=anthropic;openai;google;azure;bedrock;openrouter;openaicompat
type ProviderType string

const (
	ProviderTypeAnthropic    ProviderType = "anthropic"
	ProviderTypeOpenAI       ProviderType = "openai"
	ProviderTypeGoogle       ProviderType = "google"
	ProviderTypeAzure        ProviderType = "azure"
	ProviderTypeBedrock      ProviderType = "bedrock"
	ProviderTypeOpenRouter   ProviderType = "openrouter"
	ProviderTypeOpenAICompat ProviderType = "openaicompat"
)

// ProviderPhase describes the current phase of a Provider.
type ProviderPhase string

const (
	ProviderPhasePending ProviderPhase = "Pending"
	ProviderPhaseReady   ProviderPhase = "Ready"
	ProviderPhaseFailed  ProviderPhase = "Failed"
)

// ProviderSpec defines the desired state of Provider.
type ProviderSpec struct {

	// ====================================================================
	// TYPE
	// ====================================================================

	// Type selects the Fantasy SDK backend.
	// Known values: anthropic, openai, google, azure, bedrock, openrouter, openaicompat.
	// +kubebuilder:validation:Required
	Type ProviderType `json:"type"`

	// ====================================================================
	// CREDENTIALS
	// ====================================================================

	// Secret containing the API key for this provider.
	// Required for static-key auth. Omit when using
	// spec.endpoint.oauth2ClientCredentials, which delegates auth to the
	// token-injector sidecar.
	// +optional
	ApiKeySecret *SecretKeyRef `json:"apiKeySecret,omitempty"`

	// ====================================================================
	// ENDPOINT
	// ====================================================================

	// Endpoint configuration (optional, defaults per type).
	// +optional
	Endpoint *ProviderEndpoint `json:"endpoint,omitempty"`

	// ====================================================================
	// TYPE-SPECIFIC CONFIG
	// ====================================================================

	// Provider-type-specific configuration.
	// Only the fields relevant to spec.type are used; others are ignored.
	// +optional
	Config *ProviderConfig `json:"config,omitempty"`

	// ====================================================================
	// PER-CALL DEFAULTS
	// ====================================================================

	// Default per-call options applied to all agents using this provider.
	// Agents can override these via providerRefs[].overrides.
	// +optional
	Defaults *ProviderCallDefaults `json:"defaults,omitempty"`
}

// ProviderEndpoint configures the API endpoint.
type ProviderEndpoint struct {
	// Base URL override. Empty uses the SDK default for the provider type.
	// When OAuth2ClientCredentials is set, this is the upstream target URL
	// the token-injector sidecar will forward requests to; the agent itself
	// will be pointed at http://localhost:<sidecar-port>.
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// Custom HTTP headers injected into every API request.
	// Useful for proxy auth, rate-limit tokens, observability headers.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// OAuth2ClientCredentials enables an OAuth2 client_credentials token-injector
	// sidecar in front of the LLM API. When set, the operator injects a sidecar
	// that performs the token exchange against TokenURL and forwards requests to
	// BaseURL with a fresh bearer token. The agent container is pointed at the
	// sidecar via localhost.
	// +optional
	OAuth2ClientCredentials *OAuth2ClientCredentials `json:"oauth2ClientCredentials,omitempty"`
}

// OAuth2ClientCredentials configures an OAuth2 client_credentials grant
// performed by the token-injector sidecar. The sidecar fetches an access
// token from TokenURL using the referenced client_id / client_secret and
// injects it as a Bearer token on requests forwarded to the provider's
// BaseURL.
type OAuth2ClientCredentials struct {
	// Token endpoint (OIDC / OAuth2) used for the client_credentials grant.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TokenURL string `json:"tokenURL"`

	// Secret containing the OAuth client ID.
	ClientIDSecret SecretKeyRef `json:"clientIdSecret"`

	// Secret containing the OAuth client secret.
	ClientSecretSecret SecretKeyRef `json:"clientSecretSecret"`

	// Optional space-separated OAuth scopes.
	// +optional
	Scope string `json:"scope,omitempty"`

	// Optional OAuth audience parameter.
	// +optional
	Audience string `json:"audience,omitempty"`
}

// ProviderConfig holds type-specific provider configuration.
// Only the fields relevant to spec.type are used; others are ignored.
type ProviderConfig struct {

	// --- OpenAI-specific ---

	// OpenAI organization ID (sets OpenAI-Organization header).
	// Applies to: openai.
	// +optional
	Organization string `json:"organization,omitempty"`

	// OpenAI project ID (sets OpenAI-Project header).
	// Applies to: openai.
	// +optional
	Project string `json:"project,omitempty"`

	// Use the OpenAI Responses API for models that support it.
	// Applies to: openai, azure, openaicompat.
	// +optional
	UseResponsesAPI bool `json:"useResponsesAPI,omitempty"`

	// --- Azure-specific ---

	// Azure API version. Default: "2025-01-01-preview".
	// Applies to: azure.
	// +optional
	AzureAPIVersion string `json:"azureAPIVersion,omitempty"`

	// --- Vertex AI ---
	// Applies to: anthropic (via Vertex), google (via Vertex).

	// +optional
	Vertex *VertexConfig `json:"vertex,omitempty"`

	// --- Bedrock ---
	// Applies to: anthropic (via Bedrock), bedrock.

	// Enable AWS Bedrock routing.
	// +optional
	Bedrock bool `json:"bedrock,omitempty"`
}

// VertexConfig configures Google Cloud Vertex AI.
type VertexConfig struct {
	// GCP project ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// GCP region (e.g. "us-central1").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Location string `json:"location"`
}

// ProviderCallDefaults holds per-call options that the runtime merges
// into every Fantasy Call.ProviderOptions for this provider.
// Agent-level overrides take precedence.
type ProviderCallDefaults struct {
	// Anthropic-specific call options.
	// +optional
	Anthropic *AnthropicCallDefaults `json:"anthropic,omitempty"`

	// OpenAI-specific call options.
	// +optional
	OpenAI *OpenAICallDefaults `json:"openai,omitempty"`

	// Google-specific call options.
	// +optional
	Google *GoogleCallDefaults `json:"google,omitempty"`
}

// AnthropicCallDefaults configures Anthropic-specific per-call behavior.
type AnthropicCallDefaults struct {
	// Output effort level: low, medium, high, max.
	// +optional
	// +kubebuilder:validation:Enum=low;medium;high;max
	Effort string `json:"effort,omitempty"`

	// Extended thinking budget in tokens.
	// +optional
	// +kubebuilder:validation:Minimum=1024
	ThinkingBudgetTokens *int64 `json:"thinkingBudgetTokens,omitempty"`

	// Disable parallel tool use.
	// +optional
	DisableParallelToolUse *bool `json:"disableParallelToolUse,omitempty"`
}

// OpenAICallDefaults configures OpenAI-specific per-call behavior.
type OpenAICallDefaults struct {
	// Reasoning effort: none, minimal, low, medium, high, xhigh.
	// +optional
	// +kubebuilder:validation:Enum=none;minimal;low;medium;high;xhigh
	ReasoningEffort string `json:"reasoningEffort,omitempty"`

	// Service tier selection (e.g. "default", "flex").
	// +optional
	ServiceTier string `json:"serviceTier,omitempty"`
}

// GoogleCallDefaults configures Google-specific per-call behavior.
type GoogleCallDefaults struct {
	// Thinking level: LOW, MEDIUM, HIGH, MINIMAL.
	// +optional
	// +kubebuilder:validation:Enum=LOW;MEDIUM;HIGH;MINIMAL
	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	// Thinking budget in tokens. Mutually exclusive with ThinkingLevel.
	// +optional
	// +kubebuilder:validation:Minimum=128
	ThinkingBudgetTokens *int64 `json:"thinkingBudgetTokens,omitempty"`

	// Safety settings applied to all calls.
	// +optional
	SafetySettings []GoogleSafetySetting `json:"safetySettings,omitempty"`
}

// GoogleSafetySetting configures a safety threshold for a harm category.
type GoogleSafetySetting struct {
	// Harm category (e.g. "HARM_CATEGORY_DANGEROUS_CONTENT").
	Category string `json:"category"`
	// Block threshold (e.g. "BLOCK_ONLY_HIGH").
	Threshold string `json:"threshold"`
}

// ProviderStatus defines the observed state of Provider.
type ProviderStatus struct {
	// Current phase: Pending, Ready, Failed.
	// +optional
	Phase ProviderPhase `json:"phase,omitempty"`

	// Human-readable status message.
	// +optional
	Message string `json:"message,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Number of Agent CRs currently referencing this provider.
	// +optional
	BoundAgents int `json:"boundAgents,omitempty"`
}

// Condition types for Provider.
const (
	// ProviderConditionSecretReady indicates the referenced Secret exists and contains the expected key.
	ProviderConditionSecretReady = "SecretReady"
	// ProviderConditionConfigValid indicates the config fields are consistent with spec.type.
	ProviderConditionConfigValid = "ConfigValid"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=prov
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Agents",type=integer,JSONPath=`.status.boundAgents`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Provider configures an LLM provider that Agents reference.
// Providers are namespace-scoped — agents and providers must share a namespace.
// Extracts credentials, endpoint, and per-call configuration from Agent CRs
// into a shared, reusable resource that maps to Fantasy SDK provider backends.
type Provider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderSpec   `json:"spec,omitempty"`
	Status ProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderList contains a list of Provider.
type ProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provider `json:"items"`
}

