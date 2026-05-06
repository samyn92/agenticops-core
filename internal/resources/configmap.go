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
	"encoding/json"
	"fmt"
	"strings"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ====================================================================
// Engram Memory Protocol
// ====================================================================

// engramMemoryProtocol is the behavioral instruction block for the platform
// protocol when memory is enabled (spec.memory.serverRef is set). It teaches
// the agent when to use the mem_save, mem_search, and mem_context tools.
//
// NOTE: As of the platform protocol refactor, this is no longer appended to
// the user's systemPrompt. It is emitted as part of the separate
// platformProtocol field in config.json.
//
// Adapted from the canonical Engram Memory Protocol:
// https://github.com/Gentleman-Programming/engram
// memoryProtocolHeader is always included when memory is enabled.
const memoryProtocolHeader = `
## Memory System

You have persistent memory (survives restarts). Context from past sessions is auto-injected before each turn.
`

// memoryProtocolSave is included when autoSave is enabled.
const memoryProtocolSave = `
### Saving — call mem_save after:
- Bug fixes, architecture/design decisions, non-obvious discoveries
- Config changes, established patterns, user preferences

Format: type (bugfix|decision|discovery|pattern|config|preference|learning), title (verb + what, searchable), content (what/why/where, plus gotchas if any), tags (keywords).

Save small focused memories as you go — don't batch.
`

// memoryProtocolSaveDisabled is included when autoSave is disabled.
const memoryProtocolSaveDisabled = `
### Saving
Do NOT call mem_save. The user manages memory via the console.
`

// memoryProtocolSearch is included when autoSearch is enabled.
const memoryProtocolSearch = `
### Searching — call mem_search when:
- User references past work ("remember", "last time", "what did we do", "that bug")
- User asks "what have we done" — search first, don't guess

Ask before searching on your own initiative during troubleshooting.
Do NOT search on conversation start or for simple questions.
`

// memoryProtocolSearchDisabled is included when autoSearch is disabled.
const memoryProtocolSearchDisabled = `
### Searching
Do NOT call mem_search or mem_context. The user manages memory lookups via the console.
`

// buildMemoryProtocol assembles the memory protocol based on the agent's
// autoSave and autoSearch settings.
func buildMemoryProtocol(memory *agentsv1alpha1.MemorySpec) string {
	autoSave := true
	if memory.AutoSave != nil {
		autoSave = *memory.AutoSave
	}
	autoSearch := true
	if memory.AutoSearch != nil {
		autoSearch = *memory.AutoSearch
	}

	protocol := memoryProtocolHeader

	if autoSave {
		protocol += memoryProtocolSave
	} else {
		protocol += memoryProtocolSaveDisabled
	}

	if autoSearch {
		protocol += memoryProtocolSearch
	} else {
		protocol += memoryProtocolSearchDisabled
	}

	return protocol
}

// ====================================================================
// Delegation Protocol
// ====================================================================

// delegationProtocolTeamHeader is the delegation protocol for team-based delegation.
// Strategy/workflow details belong in the agent's systemPrompt, not here.
const delegationProtocolTeamHeader = `
## Delegation

You coordinate work by delegating to your team of specialized agents. Use list_task_agents to see who is available. Use run_agent for single tasks or run_agents for parallel fan-out of independent subtasks.

When writing delegation prompts, include:
- Clear task description with acceptance criteria
- Relevant context the agent needs (not your full conversation)
- Specific resource references (repo, branch) when applicable
`

// buildDelegationProtocol generates the delegation protocol text from DelegationSpec.
func buildDelegationProtocol(delegation *agentsv1alpha1.DelegationSpec) string {
	if delegation == nil || len(delegation.Team) == 0 {
		return ""
	}

	protocol := delegationProtocolTeamHeader

	// List team members so the agent knows who is available without calling list_task_agents.
	protocol += "\nYour team:\n"
	for _, member := range delegation.Team {
		protocol += fmt.Sprintf("  - %s\n", member)
	}

	if delegation.MaxFanOut > 0 && delegation.MaxFanOut < 10 {
		protocol += fmt.Sprintf("\nMaximum parallel delegations per fan-out: %d.\n", delegation.MaxFanOut)
	}

	return protocol
}

// ====================================================================
// Platform Protocol (combines identity + delegation + memory)
// ====================================================================

// buildPlatformProtocol assembles the full platform protocol from the agent's
// identity, delegation spec, and memory spec. This is emitted as a separate
// field in config.json and injected by the runtime as a dedicated system
// message part — the user's systemPrompt is NEVER mutated.
func buildPlatformProtocol(agent *agentsv1alpha1.Agent) string {
	var parts []string

	// ── Agent Identity ──
	identity := fmt.Sprintf("You are %s, a %s agent in the %s namespace.",
		agent.Name, string(agent.Spec.Mode), agent.Namespace)
	parts = append(parts, identity)

	// ── Delegation Protocol ──
	if delegation := buildDelegationProtocol(agent.Spec.Delegation); delegation != "" {
		parts = append(parts, delegation)
	}

	// ── Memory Protocol ──
	if agent.Spec.Memory != nil {
		parts = append(parts, buildMemoryProtocol(agent.Spec.Memory))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n")
}

// ====================================================================
// Config types
// ====================================================================

// ToolEntry describes a tool package path.
type ToolEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	UIHint      string `json:"uiHint,omitempty"`
}

// MCPEntry describes an MCP server binding.
type MCPEntry struct {
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	DirectTools []string `json:"directTools,omitempty"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	UIHint      string   `json:"uiHint,omitempty"`
}

// ProviderEntry describes a configured provider in config.json.
// The runtime uses the Type field for type-based SDK dispatch with full
// option wiring (base URL, headers, Vertex/Bedrock config, Responses API,
// and per-call defaults).
type ProviderEntry struct {
	Name            string                     `json:"name"`
	Type            string                     `json:"type,omitempty"`
	BaseURL         string                     `json:"baseURL,omitempty"`
	Headers         map[string]string          `json:"headers,omitempty"`
	Organization    string                     `json:"organization,omitempty"`
	Project         string                     `json:"project,omitempty"`
	UseResponsesAPI bool                       `json:"useResponsesAPI,omitempty"`
	AzureAPIVersion string                     `json:"azureAPIVersion,omitempty"`
	Vertex          *ProviderVertexEntry       `json:"vertex,omitempty"`
	Bedrock         bool                       `json:"bedrock,omitempty"`
	CallDefaults    *ProviderCallDefaultsEntry `json:"callDefaults,omitempty"`
}

// ProviderVertexEntry holds Vertex AI config for config.json.
type ProviderVertexEntry struct {
	Project  string `json:"project"`
	Location string `json:"location"`
}

// ProviderCallDefaultsEntry holds per-call defaults for config.json.
type ProviderCallDefaultsEntry struct {
	Anthropic *AnthropicCallDefaultsEntry `json:"anthropic,omitempty"`
	OpenAI    *OpenAICallDefaultsEntry    `json:"openai,omitempty"`
	Google    *GoogleCallDefaultsEntry    `json:"google,omitempty"`
}

// AnthropicCallDefaultsEntry holds Anthropic per-call defaults for config.json.
type AnthropicCallDefaultsEntry struct {
	Effort                 string `json:"effort,omitempty"`
	ThinkingBudgetTokens   *int64 `json:"thinkingBudgetTokens,omitempty"`
	DisableParallelToolUse *bool  `json:"disableParallelToolUse,omitempty"`
}

// OpenAICallDefaultsEntry holds OpenAI per-call defaults for config.json.
type OpenAICallDefaultsEntry struct {
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	ServiceTier     string `json:"serviceTier,omitempty"`
}

// GoogleCallDefaultsEntry holds Google per-call defaults for config.json.
type GoogleCallDefaultsEntry struct {
	ThinkingLevel        string                     `json:"thinkingLevel,omitempty"`
	ThinkingBudgetTokens *int64                     `json:"thinkingBudgetTokens,omitempty"`
	SafetySettings       []GoogleSafetySettingEntry `json:"safetySettings,omitempty"`
}

// GoogleSafetySettingEntry holds a Google safety setting for config.json.
type GoogleSafetySettingEntry struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// ToolHooksEntry holds runtime hook config.
type ToolHooksEntry struct {
	BlockedCommands    []string            `json:"blockedCommands,omitempty"`
	AllowedPaths       []string            `json:"allowedPaths,omitempty"`
	AuditTools         []string            `json:"auditTools,omitempty"`
	MemorySaveRules    []MemorySaveRule    `json:"memorySaveRules,omitempty"`
	ContextInjectTools []ContextInjectRule `json:"contextInjectTools,omitempty"`
}

// MemorySaveRule mirrors the CRD spec for config.json serialization.
type MemorySaveRule struct {
	Tool        string            `json:"tool"`
	MatchOutput string            `json:"matchOutput,omitempty"`
	MatchArgs   map[string]string `json:"matchArgs,omitempty"`
	Type        string            `json:"type,omitempty"`
	Scope       string            `json:"scope,omitempty"`
}

// ContextInjectRule mirrors the CRD spec for config.json serialization.
type ContextInjectRule struct {
	Tool  string `json:"tool"`
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// ContextEntry describes a context file.
type ContextEntry struct {
	Path string `json:"path"`
}

// IntegrationEntry describes a resource binding for the runtime config.
type IntegrationEntry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	AutoContext bool   `json:"autoContext,omitempty"`

	// Kind-specific config (one of these will be set)
	GitHub        *IntegrationGitHubEntry        `json:"github,omitempty"`
	GitHubOrg     *IntegrationGitHubOrgEntry     `json:"githubOrg,omitempty"`
	GitLab        *IntegrationGitLabEntry        `json:"gitlab,omitempty"`
	GitLabGroup   *IntegrationGitLabGroupEntry   `json:"gitlabGroup,omitempty"`
	Git           *IntegrationGitEntry           `json:"git,omitempty"`
	S3            *IntegrationS3Entry            `json:"s3,omitempty"`
	Documentation *IntegrationDocumentationEntry `json:"documentation,omitempty"`
}

// IntegrationGitHubEntry holds GitHub repo config for the runtime.
type IntegrationGitHubEntry struct {
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	APIURL        string `json:"apiURL,omitempty"`
}

// IntegrationGitHubOrgEntry holds GitHub org config for the runtime.
type IntegrationGitHubOrgEntry struct {
	Org        string   `json:"org"`
	RepoFilter []string `json:"repoFilter,omitempty"`
	APIURL     string   `json:"apiURL,omitempty"`
}

// IntegrationGitLabEntry holds GitLab project config for the runtime.
type IntegrationGitLabEntry struct {
	BaseURL       string `json:"baseURL"`
	Project       string `json:"project"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

// IntegrationGitLabGroupEntry holds GitLab group config for the runtime.
type IntegrationGitLabGroupEntry struct {
	BaseURL  string   `json:"baseURL"`
	Group    string   `json:"group"`
	Projects []string `json:"projects,omitempty"`
}

// IntegrationGitEntry holds plain git repo config for the runtime.
type IntegrationGitEntry struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
}

// IntegrationS3Entry holds S3 bucket config for the runtime.
type IntegrationS3Entry struct {
	Bucket   string `json:"bucket"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// IntegrationDocumentationEntry holds documentation config for the runtime.
type IntegrationDocumentationEntry struct {
	URLs []string `json:"urls,omitempty"`
}

// MemoryConfigEntry holds memory configuration for the runtime.
type MemoryConfigEntry struct {
	ServerURL     string `json:"serverURL"`
	Project       string `json:"project"`
	ContextLimit  int    `json:"contextLimit"`
	AutoSummarize bool   `json:"autoSummarize"`
	AutoSave      bool   `json:"autoSave"`
	AutoSearch    bool   `json:"autoSearch"`
}

// DelegationConfigEntry holds delegation config for the runtime.
// The runtime uses Team for access control and MaxFanOut for batch size limits.
type DelegationConfigEntry struct {
	Team      []string `json:"team"`
	MaxFanOut int      `json:"maxFanOut,omitempty"`
}

// AgentConfig is the JSON structure mounted at /etc/operator/config.json for the Fantasy runtime.
type AgentConfig struct {
	Runtime            string                 `json:"runtime"`
	Providers          []ProviderEntry        `json:"providers"`
	PrimaryProvider    string                 `json:"primaryProvider,omitempty"`
	PrimaryModel       string                 `json:"primaryModel"`
	FallbackModels     []string               `json:"fallbackModels,omitempty"`
	TitleModel         string                 `json:"titleModel,omitempty"`
	SystemPrompt       string                 `json:"systemPrompt,omitempty"`
	PlatformProtocol   string                 `json:"platformProtocol,omitempty"`
	BuiltinTools       []string               `json:"builtinTools"`
	Tools              []ToolEntry            `json:"tools"`
	MCPServers         []MCPEntry             `json:"mcpServers,omitempty"`
	ToolHooks          *ToolHooksEntry        `json:"toolHooks,omitempty"`
	ContextFiles       []ContextEntry         `json:"contextFiles,omitempty"`
	Temperature        *float64               `json:"temperature,omitempty"`
	MaxOutputTokens    *int64                 `json:"maxOutputTokens,omitempty"`
	MaxSteps           *int                   `json:"maxSteps,omitempty"`
	MaxToolResultChars int                    `json:"maxToolResultChars,omitempty"`
	BudgetFraction     *float64               `json:"budgetFraction,omitempty"`
	PermissionTools    []string               `json:"permissionTools,omitempty"`
	EnableQuestionTool bool                   `json:"enableQuestionTool,omitempty"`
	Resources          []IntegrationEntry     `json:"resources,omitempty"`
	Memory             *MemoryConfigEntry     `json:"memory,omitempty"`
	Delegation         *DelegationConfigEntry `json:"delegation,omitempty"`
}

// ====================================================================
// ConfigMap builder
// ====================================================================

// BuildAgentConfigMap generates the operator extension ConfigMap from an Agent spec.
// agentTools is the resolved list of AgentTool CRs (used to look up the memory server URL
// and to build tool/MCP entries).
// providers is the resolved list of Provider CRs referenced by the agent via providerRefs.
func BuildAgentConfigMap(agent *agentsv1alpha1.Agent, integrations []agentsv1alpha1.Integration, agentTools []agentsv1alpha1.AgentTool, providers []agentsv1alpha1.Provider) (*corev1.ConfigMap, error) {
	config := AgentConfig{
		Runtime:            "fantasy",
		PrimaryModel:       agent.Spec.Model,
		PrimaryProvider:    agent.Spec.PrimaryProvider,
		TitleModel:         agent.Spec.TitleModel,
		SystemPrompt:       agent.Spec.SystemPrompt,
		BuiltinTools:       agent.Spec.BuiltinTools,
		Temperature:        agent.Spec.Temperature,
		MaxOutputTokens:    agent.Spec.MaxOutputTokens,
		MaxSteps:           agent.Spec.MaxSteps,
		PermissionTools:    agent.Spec.PermissionTools,
		EnableQuestionTool: agent.Spec.EnableQuestionTool,
	}

	// Memory (Engram integration)
	if agent.Spec.Memory != nil {
		serverURL := resolveMemoryServerURL(agent.Spec.Memory.ServerRef, agent.Namespace, agentTools)
		if serverURL != "" {
			project := agent.Spec.Memory.Project
			if project == "" {
				project = agent.Name
			}
			contextLimit := agent.Spec.Memory.ContextLimit
			if contextLimit == 0 {
				contextLimit = 5
			}
			autoSummarize := true
			if agent.Spec.Memory.AutoSummarize != nil {
				autoSummarize = *agent.Spec.Memory.AutoSummarize
			}
			autoSave := true
			if agent.Spec.Memory.AutoSave != nil {
				autoSave = *agent.Spec.Memory.AutoSave
			}
			autoSearch := true
			if agent.Spec.Memory.AutoSearch != nil {
				autoSearch = *agent.Spec.Memory.AutoSearch
			}
			config.Memory = &MemoryConfigEntry{
				ServerURL:     serverURL,
				Project:       project,
				ContextLimit:  contextLimit,
				AutoSummarize: autoSummarize,
				AutoSave:      autoSave,
				AutoSearch:    autoSearch,
			}
		}
	}

	// Platform protocol: identity + delegation + memory protocol.
	// This is a SEPARATE field — the user's systemPrompt is never mutated.
	if protocol := buildPlatformProtocol(agent); protocol != "" {
		config.PlatformProtocol = protocol
	}

	// Delegation config (for runtime enforcement of team + maxFanOut)
	if agent.Spec.Delegation != nil {
		config.Delegation = &DelegationConfigEntry{
			Team:      agent.Spec.Delegation.Team,
			MaxFanOut: agent.Spec.Delegation.MaxFanOut,
		}
	}

	// Build tool map for lookups
	toolMap := make(map[string]*agentsv1alpha1.AgentTool, len(agentTools))
	for i := range agentTools {
		toolMap[agentTools[i].Name] = &agentTools[i]
	}

	// Tools section: iterate agent.Spec.Tools and look up each AgentTool
	mcpIndex := 0
	for _, binding := range agent.Spec.Tools {
		tool := toolMap[binding.Name]
		if tool == nil {
			continue
		}

		switch {
		case tool.Spec.OCI != nil, tool.Spec.ConfigMap != nil, tool.Spec.Inline != nil:
			// OCI, configMap, inline → ToolEntry
			path := fmt.Sprintf("%s/%s", MountTools, binding.Name)
			config.Tools = append(config.Tools, ToolEntry{
				Name:        binding.Name,
				Path:        path,
				Description: tool.Spec.Description,
				Category:    tool.Spec.Category,
				UIHint:      tool.Spec.UIHint,
			})

		case tool.IsMCPSource():
			// mcpServer, mcpEndpoint → MCPEntry (gateway port assignment by index)
			port := GatewayBasePort + mcpIndex
			config.MCPServers = append(config.MCPServers, MCPEntry{
				Name:        binding.Name,
				Port:        port,
				DirectTools: binding.DirectTools,
				Description: tool.Spec.Description,
				Category:    tool.Spec.Category,
				UIHint:      tool.Spec.UIHint,
			})
			mcpIndex++

		case tool.Spec.Skill != nil:
			// skill → ContextEntry
			path := fmt.Sprintf("%s/%s", MountTools, binding.Name)
			config.ContextFiles = append(config.ContextFiles, ContextEntry{Path: path})
		}
	}

	// Providers: build enriched entries from Provider CRs (providerRefs).
	providerBindingMap := make(map[string]*agentsv1alpha1.ProviderBinding, len(agent.Spec.ProviderRefs))
	for i := range agent.Spec.ProviderRefs {
		providerBindingMap[agent.Spec.ProviderRefs[i].Name] = &agent.Spec.ProviderRefs[i]
	}
	for i, prov := range providers {
		entry := buildProviderEntry(&prov, providerBindingMap[prov.Name], i)
		config.Providers = append(config.Providers, entry)
	}

	// Fallback models
	config.FallbackModels = agent.Spec.FallbackModels

	// Tool hooks
	if agent.Spec.ToolHooks != nil {
		config.ToolHooks = &ToolHooksEntry{
			BlockedCommands: agent.Spec.ToolHooks.BlockedCommands,
			AllowedPaths:    agent.Spec.ToolHooks.AllowedPaths,
			AuditTools:      agent.Spec.ToolHooks.AuditTools,
		}
		// Memory save rules
		for _, r := range agent.Spec.ToolHooks.MemorySaveRules {
			config.ToolHooks.MemorySaveRules = append(config.ToolHooks.MemorySaveRules, MemorySaveRule{
				Tool:        r.Tool,
				MatchOutput: r.MatchOutput,
				MatchArgs:   r.MatchArgs,
				Type:        r.Type,
				Scope:       r.Scope,
			})
		}
		// Context inject rules
		for _, r := range agent.Spec.ToolHooks.ContextInjectTools {
			config.ToolHooks.ContextInjectTools = append(config.ToolHooks.ContextInjectTools, ContextInjectRule{
				Tool:  r.Tool,
				Query: r.Query,
				Limit: r.Limit,
			})
		}
	}

	// Resources (Integration bindings)
	bindingMap := make(map[string]agentsv1alpha1.IntegrationBinding)
	for _, b := range agent.Spec.Integrations {
		bindingMap[b.Name] = b
	}
	for _, res := range integrations {
		binding := bindingMap[res.Name]
		entry := IntegrationEntry{
			Name:        res.Name,
			Kind:        string(res.Spec.Kind),
			DisplayName: res.Spec.DisplayName,
			Description: res.Spec.Description,
			ReadOnly:    binding.ReadOnly,
			AutoContext: binding.AutoContext,
		}
		mapIntegrationKind(&entry, &res)
		config.Resources = append(config.Resources, entry)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal operator config: %w", err)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "config"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "config"),
		},
		Data: map[string]string{
			"config.json": string(data),
		},
	}, nil
}

// buildProviderEntry creates an enriched ProviderEntry from a Provider CR,
// merging any per-agent overrides from the ProviderBinding.
// providerIndex is the position of this provider in the agent's resolved
// providers slice; it is used to compute the token-injector sidecar port
// when OAuth2 client_credentials is configured.
func buildProviderEntry(prov *agentsv1alpha1.Provider, binding *agentsv1alpha1.ProviderBinding, providerIndex int) ProviderEntry {
	entry := ProviderEntry{
		Name: prov.Name,
		Type: string(prov.Spec.Type),
	}

	// Endpoint
	if prov.Spec.Endpoint != nil {
		entry.BaseURL = prov.Spec.Endpoint.BaseURL
		entry.Headers = prov.Spec.Endpoint.Headers

		// When an OAuth2 token-injector sidecar is configured, redirect the
		// agent to the localhost sidecar instead of the upstream URL. The
		// sidecar holds the real BaseURL as its TARGET_URL.
		if prov.Spec.Endpoint.OAuth2ClientCredentials != nil && entry.BaseURL != "" {
			entry.BaseURL = fmt.Sprintf("http://localhost:%d", TokenInjectorPort(providerIndex))
		}
	}

	// Type-specific config
	if prov.Spec.Config != nil {
		cfg := prov.Spec.Config
		entry.Organization = cfg.Organization
		entry.Project = cfg.Project
		entry.UseResponsesAPI = cfg.UseResponsesAPI
		entry.AzureAPIVersion = cfg.AzureAPIVersion
		entry.Bedrock = cfg.Bedrock
		if cfg.Vertex != nil {
			entry.Vertex = &ProviderVertexEntry{
				Project:  cfg.Vertex.Project,
				Location: cfg.Vertex.Location,
			}
		}
	}

	// Per-call defaults: start from Provider CR defaults
	var callDefaults *ProviderCallDefaultsEntry
	if prov.Spec.Defaults != nil {
		callDefaults = convertCallDefaults(prov.Spec.Defaults)
	}

	// Merge agent-level overrides from ProviderBinding
	if binding != nil && binding.Overrides != nil {
		overrides := convertCallDefaults(binding.Overrides)
		callDefaults = mergeCallDefaults(callDefaults, overrides)
	}

	entry.CallDefaults = callDefaults
	return entry
}

// convertCallDefaults converts CRD call defaults to config.json format.
func convertCallDefaults(src *agentsv1alpha1.ProviderCallDefaults) *ProviderCallDefaultsEntry {
	if src == nil {
		return nil
	}
	result := &ProviderCallDefaultsEntry{}
	if src.Anthropic != nil {
		result.Anthropic = &AnthropicCallDefaultsEntry{
			Effort:                 src.Anthropic.Effort,
			ThinkingBudgetTokens:   src.Anthropic.ThinkingBudgetTokens,
			DisableParallelToolUse: src.Anthropic.DisableParallelToolUse,
		}
	}
	if src.OpenAI != nil {
		result.OpenAI = &OpenAICallDefaultsEntry{
			ReasoningEffort: src.OpenAI.ReasoningEffort,
			ServiceTier:     src.OpenAI.ServiceTier,
		}
	}
	if src.Google != nil {
		result.Google = &GoogleCallDefaultsEntry{
			ThinkingLevel:        src.Google.ThinkingLevel,
			ThinkingBudgetTokens: src.Google.ThinkingBudgetTokens,
		}
		for _, ss := range src.Google.SafetySettings {
			result.Google.SafetySettings = append(result.Google.SafetySettings, GoogleSafetySettingEntry{
				Category:  ss.Category,
				Threshold: ss.Threshold,
			})
		}
	}
	return result
}

// mergeCallDefaults merges override values on top of base defaults.
// Non-zero override fields win over base fields.
func mergeCallDefaults(base, override *ProviderCallDefaultsEntry) *ProviderCallDefaultsEntry {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	result := *base // shallow copy

	// Anthropic overrides
	if override.Anthropic != nil {
		if result.Anthropic == nil {
			result.Anthropic = override.Anthropic
		} else {
			merged := *result.Anthropic
			if override.Anthropic.Effort != "" {
				merged.Effort = override.Anthropic.Effort
			}
			if override.Anthropic.ThinkingBudgetTokens != nil {
				merged.ThinkingBudgetTokens = override.Anthropic.ThinkingBudgetTokens
			}
			if override.Anthropic.DisableParallelToolUse != nil {
				merged.DisableParallelToolUse = override.Anthropic.DisableParallelToolUse
			}
			result.Anthropic = &merged
		}
	}

	// OpenAI overrides
	if override.OpenAI != nil {
		if result.OpenAI == nil {
			result.OpenAI = override.OpenAI
		} else {
			merged := *result.OpenAI
			if override.OpenAI.ReasoningEffort != "" {
				merged.ReasoningEffort = override.OpenAI.ReasoningEffort
			}
			if override.OpenAI.ServiceTier != "" {
				merged.ServiceTier = override.OpenAI.ServiceTier
			}
			result.OpenAI = &merged
		}
	}

	// Google overrides
	if override.Google != nil {
		if result.Google == nil {
			result.Google = override.Google
		} else {
			merged := *result.Google
			if override.Google.ThinkingLevel != "" {
				merged.ThinkingLevel = override.Google.ThinkingLevel
			}
			if override.Google.ThinkingBudgetTokens != nil {
				merged.ThinkingBudgetTokens = override.Google.ThinkingBudgetTokens
			}
			if len(override.Google.SafetySettings) > 0 {
				merged.SafetySettings = override.Google.SafetySettings
			}
			result.Google = &merged
		}
	}

	return &result
}

// BuildAgentRunConfigMap creates a per-run ConfigMap that extends the base agent
// config with git tool entries. The runtime discovers these via loadOCITools()
// and spawns the tool binary directly via stdio — no gateway sidecar needed.
func BuildAgentRunConfigMap(baseConfigMap *corev1.ConfigMap, runName string, gitToolEntries []ToolEntry) (*corev1.ConfigMap, error) {
	configJSON := baseConfigMap.Data["config.json"]
	var config AgentConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, fmt.Errorf("unmarshal base config: %w", err)
	}

	// Deduplicate: skip git tool entries that already exist in the base config.
	// The agent spec may already reference the same tool (e.g. gitlab) that the
	// git workspace also injects — appending blindly causes duplicate MCP servers.
	existing := make(map[string]struct{}, len(config.Tools))
	for _, t := range config.Tools {
		existing[t.Name] = struct{}{}
	}
	for _, gt := range gitToolEntries {
		if _, dup := existing[gt.Name]; !dup {
			config.Tools = append(config.Tools, gt)
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal run config: %w", err)
	}

	labels := make(map[string]string)
	for k, v := range baseConfigMap.Labels {
		labels[k] = v
	}
	labels["agents.agentops.io/run"] = runName

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runName + "-config",
			Namespace: baseConfigMap.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"config.json": string(data),
		},
	}, nil
}

// mapIntegrationKind populates the kind-specific fields of an IntegrationEntry
// based on the Integration spec. Extracted to reduce cyclomatic complexity of
// BuildAgentConfigMap.
func mapIntegrationKind(entry *IntegrationEntry, res *agentsv1alpha1.Integration) {
	switch res.Spec.Kind {
	case agentsv1alpha1.IntegrationKindGitHubRepo:
		if res.Spec.GitHub != nil {
			entry.GitHub = &IntegrationGitHubEntry{
				Owner:         res.Spec.GitHub.Owner,
				Repo:          res.Spec.GitHub.Repo,
				DefaultBranch: res.Spec.GitHub.DefaultBranch,
				APIURL:        res.Spec.GitHub.APIURL,
			}
		}
	case agentsv1alpha1.IntegrationKindGitHubOrg:
		if res.Spec.GitHubOrg != nil {
			entry.GitHubOrg = &IntegrationGitHubOrgEntry{
				Org:        res.Spec.GitHubOrg.Org,
				RepoFilter: res.Spec.GitHubOrg.RepoFilter,
				APIURL:     res.Spec.GitHubOrg.APIURL,
			}
		}
	case agentsv1alpha1.IntegrationKindGitLabProject:
		if res.Spec.GitLab != nil {
			entry.GitLab = &IntegrationGitLabEntry{
				BaseURL:       res.Spec.GitLab.BaseURL,
				Project:       res.Spec.GitLab.Project,
				DefaultBranch: res.Spec.GitLab.DefaultBranch,
			}
		}
	case agentsv1alpha1.IntegrationKindGitLabGroup:
		if res.Spec.GitLabGroup != nil {
			entry.GitLabGroup = &IntegrationGitLabGroupEntry{
				BaseURL:  res.Spec.GitLabGroup.BaseURL,
				Group:    res.Spec.GitLabGroup.Group,
				Projects: res.Spec.GitLabGroup.Projects,
			}
		}
	case agentsv1alpha1.IntegrationKindGitRepo:
		if res.Spec.Git != nil {
			entry.Git = &IntegrationGitEntry{
				URL:    res.Spec.Git.URL,
				Branch: res.Spec.Git.Branch,
			}
		}
	case agentsv1alpha1.IntegrationKindS3Bucket:
		if res.Spec.S3 != nil {
			entry.S3 = &IntegrationS3Entry{
				Bucket:   res.Spec.S3.Bucket,
				Region:   res.Spec.S3.Region,
				Endpoint: res.Spec.S3.Endpoint,
				Prefix:   res.Spec.S3.Prefix,
			}
		}
	case agentsv1alpha1.IntegrationKindDocumentation:
		if res.Spec.Documentation != nil {
			entry.Documentation = &IntegrationDocumentationEntry{
				URLs: res.Spec.Documentation.URLs,
			}
		}
	}
}

// resolveMemoryServerURL determines the HTTP URL for the memory (Engram) server.
// It checks the resolved AgentTool list for a matching serverRef name with an
// mcpServer/mcpEndpoint source; if found, it uses the tool's status ServiceURL
// or computes it via AgentToolServiceURL. Otherwise, it assumes the server is
// deployed manually (e.g., plain K8s Deployment+Service) and constructs a
// conventional in-cluster URL: http://<serverRef>.<namespace>.svc:7437
func resolveMemoryServerURL(serverRef string, namespace string, agentTools []agentsv1alpha1.AgentTool) string {
	if serverRef == "" {
		return ""
	}
	// Check if serverRef matches a known AgentTool CR with MCP source
	for i := range agentTools {
		if agentTools[i].Name == serverRef && agentTools[i].IsMCPSource() {
			if agentTools[i].Status.ServiceURL != "" {
				return agentTools[i].Status.ServiceURL
			}
			return AgentToolServiceURL(&agentTools[i])
		}
	}
	// Fallback: manually deployed service (convention: name.namespace.svc:7437)
	const engramDefaultPort = 7437
	return fmt.Sprintf("http://%s.%s.svc:%d", serverRef, namespace, engramDefaultPort)
}

// ====================================================================
// Gateway & MCP ConfigMaps
// ====================================================================

// BuildGatewayConfigMap generates the MCP gateway permission rules ConfigMap.
// Only created when the agent has MCP-source tool bindings.
func BuildGatewayConfigMap(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool) (*corev1.ConfigMap, error) {
	// Build tool map for lookups
	toolMap := make(map[string]*agentsv1alpha1.AgentTool, len(agentTools))
	for i := range agentTools {
		toolMap[agentTools[i].Name] = &agentTools[i]
	}

	// Build per-server permission rules from MCP-source tool bindings
	rules := make(map[string]interface{})
	for _, binding := range agent.Spec.Tools {
		tool := toolMap[binding.Name]
		if tool == nil || !tool.IsMCPSource() {
			continue
		}
		if binding.Permissions != nil {
			rules[binding.Name] = map[string]interface{}{
				"mode":  binding.Permissions.Mode,
				"rules": binding.Permissions.Rules,
			}
		}
	}

	if len(rules) == 0 {
		// Still create the configmap with empty rules for the gateway
		rules["_empty"] = nil
	}

	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal gateway config: %w", err)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "gateway"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "gateway"),
		},
		Data: map[string]string{
			"permissions.json": string(data),
		},
	}, nil
}

// MCPJsonConfig is the native MCP config format consumed by the runtime's MCP adapter.
type MCPJsonConfig struct {
	MCPServers map[string]MCPJsonServerEntry `json:"mcpServers"`
}

// MCPJsonServerEntry describes one MCP server in mcp.json.
type MCPJsonServerEntry struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Lifecycle   string `json:"lifecycle"`
	IdleTimeout int    `json:"idleTimeout"`
}

// BuildMCPConfigMap generates the mcp.json ConfigMap for the runtime's MCP adapter.
// Only created when the agent has MCP-source tool bindings.
func BuildMCPConfigMap(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool) (*corev1.ConfigMap, error) {
	// Build tool map for lookups
	toolMap := make(map[string]*agentsv1alpha1.AgentTool, len(agentTools))
	for i := range agentTools {
		toolMap[agentTools[i].Name] = &agentTools[i]
	}

	mcpConfig := MCPJsonConfig{
		MCPServers: make(map[string]MCPJsonServerEntry),
	}

	mcpIndex := 0
	for _, binding := range agent.Spec.Tools {
		tool := toolMap[binding.Name]
		if tool == nil || !tool.IsMCPSource() {
			continue
		}
		port := GatewayBasePort + mcpIndex
		mcpConfig.MCPServers[binding.Name] = MCPJsonServerEntry{
			Type:        "sse",
			URL:         fmt.Sprintf("http://localhost:%d/sse", port),
			Lifecycle:   "keep-alive",
			IdleTimeout: 300,
		}
		mcpIndex++
	}

	if len(mcpConfig.MCPServers) == 0 {
		return nil, nil
	}

	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal mcp.json: %w", err)
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "mcp"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "mcp"),
		},
		Data: map[string]string{
			"mcp.json": string(data),
		},
	}, nil
}

// ProviderEnvVarName returns the standard env var name for a provider API key.
// e.g. "anthropic" -> "ANTHROPIC_API_KEY"
func ProviderEnvVarName(providerName string) string {
	return fmt.Sprintf("%s_API_KEY", strings.ToUpper(providerName))
}
