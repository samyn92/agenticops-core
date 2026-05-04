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

// AgentRunPhase describes the current phase of an AgentRun.
type AgentRunPhase string

const (
	AgentRunPhasePending   AgentRunPhase = "Pending"
	AgentRunPhaseQueued    AgentRunPhase = "Queued"
	AgentRunPhaseRunning   AgentRunPhase = "Running"
	AgentRunPhaseSucceeded AgentRunPhase = "Succeeded"
	AgentRunPhaseFailed    AgentRunPhase = "Failed"
)

// AgentRunSource describes what created this AgentRun.
// +kubebuilder:validation:Enum=channel;agent;schedule;console
type AgentRunSource string

const (
	AgentRunSourceChannel  AgentRunSource = "channel"
	AgentRunSourceAgent    AgentRunSource = "agent"
	AgentRunSourceSchedule AgentRunSource = "schedule"
	AgentRunSourceConsole  AgentRunSource = "console"
)

// AgentRunSpec defines the desired state of AgentRun.
type AgentRunSpec struct {
	// Name of the Agent CR to run.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// Prompt to send to the agent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Prompt string `json:"prompt"`

	// What created this run: channel, agent, or schedule.
	// +kubebuilder:validation:Required
	Source AgentRunSource `json:"source"`

	// Name of the source (Channel name, agent name, or "schedule").
	// +optional
	SourceRef string `json:"sourceRef,omitempty"`

	// Git workspace configuration. When set, the task agent clones the repo,
	// works on a feature branch, and can create/update a PR/MR.
	// +optional
	Git *AgentRunGitSpec `json:"git,omitempty"`

	// Outcome lets the caller hint the expected intent of the run.
	// The executing agent finalizes the actual intent in status.outcome.
	// +optional
	Outcome *AgentRunOutcomeSpec `json:"outcome,omitempty"`
}

// AgentRunIntent describes WHY a run happened. The executing agent
// finalizes this in status.outcome.intent at end-of-run; the caller
// can hint via spec.outcome.intent.
// +kubebuilder:validation:Enum=change;plan;incident;discovery;noop
type AgentRunIntent string

const (
	// IntentChange — run modified code; typical artifact: pr/mr (+ commit).
	IntentChange AgentRunIntent = "change"
	// IntentPlan — run produced a plan/spec/decision for later discussion;
	// typical artifact: issue.
	IntentPlan AgentRunIntent = "plan"
	// IntentIncident — run triaged an incident; typical artifacts: issue (RCA)
	// + memory (lesson learned).
	IntentIncident AgentRunIntent = "incident"
	// IntentDiscovery — run captured durable knowledge; typical artifact: memory.
	IntentDiscovery AgentRunIntent = "discovery"
	// IntentNoop — chit-chat, aborted run, daemon idle. No artifacts.
	IntentNoop AgentRunIntent = "noop"
)

// AgentRunOutcomeSpec lets the caller hint the expected intent when the
// run is created. The executing agent overrides this in status.outcome.intent
// if reality differs.
type AgentRunOutcomeSpec struct {
	// Hinted intent. The executing agent may override in status.outcome.intent.
	// +optional
	Intent AgentRunIntent `json:"intent,omitempty"`
}

// AgentRunArtifact describes a durable external reference produced by the run.
type AgentRunArtifact struct {
	// Kind of artifact.
	// +kubebuilder:validation:Enum=pr;mr;issue;memory;commit
	Kind string `json:"kind"`

	// Forge provider (github, gitlab, memory, ...).
	// +optional
	Provider string `json:"provider,omitempty"`

	// Fully-qualified URL — where a human clicks.
	// +optional
	URL string `json:"url,omitempty"`

	// Identifier within the provider (PR number, issue number, memory id, sha).
	// +optional
	Ref string `json:"ref,omitempty"`

	// Short human title rendered in the console Run card.
	// +optional
	Title string `json:"title,omitempty"`
}

// AgentRunOutcomeStatus is the authoritative outcome of an AgentRun,
// written by the executing agent at end-of-run.
type AgentRunOutcomeStatus struct {
	// Finalized intent. Authoritative — overrides spec.outcome.intent.
	// Console falls back to spec value when this is empty (run still in flight).
	// Set once at end-of-run; immutable thereafter.
	// +optional
	Intent AgentRunIntent `json:"intent,omitempty"`

	// Artifacts produced by the run. Append-only during the run; frozen
	// once status.phase is terminal (Succeeded or Failed).
	// +optional
	Artifacts []AgentRunArtifact `json:"artifacts,omitempty"`

	// Short summary (1-3 sentences) written by the executing agent at
	// end-of-run, for rendering in the Run sidebar card.
	// +optional
	Summary string `json:"summary,omitempty"`
}

// AgentRunGitSpec configures a git workspace for a task agent run.
type AgentRunGitSpec struct {
	// Reference to an AgentResource CR (github-repo, gitlab-project, or git-repo)
	// that provides the repository URL and credentials.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ResourceRef string `json:"resourceRef"`

	// Feature branch to work on. Created from baseBranch if it doesn't exist.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Base branch for the PR/MR target (e.g. "main"). Defaults to the repo's default branch.
	// +optional
	BaseBranch string `json:"baseBranch,omitempty"`
}

// AgentRunStatus defines the observed state of AgentRun.
type AgentRunStatus struct {
	// Current phase: Pending, Queued, Running, Succeeded, Failed.
	// +optional
	Phase AgentRunPhase `json:"phase,omitempty"`

	// Mode inherited from the target agent (task or daemon).
	// +optional
	Mode AgentMode `json:"mode,omitempty"`

	// When execution started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// When execution completed.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Job name (task mode only).
	// +optional
	JobName string `json:"jobName,omitempty"`

	// Textual output from the agent run.
	// +optional
	Output string `json:"output,omitempty"`

	// Number of tool calls made during execution.
	// +optional
	ToolCalls int `json:"toolCalls,omitempty"`

	// Total tokens consumed.
	// +optional
	TokensUsed int `json:"tokensUsed,omitempty"`

	// Estimated cost in USD.
	// +optional
	Cost string `json:"cost,omitempty"`

	// Actual model used (may differ from agent's primary if fallback triggered).
	// +optional
	Model string `json:"model,omitempty"`

	// OpenTelemetry trace ID for this run (hex-encoded 128-bit).
	// +optional
	TraceID string `json:"traceID,omitempty"`

	// Outcome is the authoritative result of the run, written by the
	// executing agent at end-of-run via the run_finish built-in tool.
	// Replaces the legacy pullRequestURL/branch/commits fields — git
	// artifacts now appear in outcome.artifacts as kind=pr/mr/commit.
	// +optional
	Outcome *AgentRunOutcomeStatus `json:"outcome,omitempty"`

	// Standard conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for AgentRun.
const (
	AgentRunConditionComplete = "Complete"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ar
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentRef`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.status.mode`
// +kubebuilder:printcolumn:name="Intent",type=string,JSONPath=`.status.outcome.intent`
// +kubebuilder:printcolumn:name="Artifact",type=string,JSONPath=`.status.outcome.artifacts[0].url`
// +kubebuilder:printcolumn:name="Tokens",type=integer,JSONPath=`.status.tokensUsed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentRun is the Schema for the agentruns API.
// Tracks one execution of an Agent. Created by Channel, run_agent tool, or schedule.
type AgentRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentRunSpec   `json:"spec,omitempty"`
	Status AgentRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentRunList contains a list of AgentRun.
type AgentRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRun `json:"items"`
}
