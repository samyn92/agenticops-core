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

// engramMemoryProtocol is the behavioral instruction block appended to
// the agent's system prompt when memory is enabled (spec.memory.serverRef
// is set). It teaches the agent when to use the mem_save, mem_search,
// and mem_context tools proactively.
//
// Adapted from the canonical Engram Memory Protocol:
// https://github.com/Gentleman-Programming/engram
// engramMemoryProtocolHeader is always included when memory is enabled.
const engramMemoryProtocolHeader = `

## Engram Persistent Memory — Protocol

You have access to Engram, a persistent memory system that survives across
pod restarts and conversation resets.
`

// engramMemoryProtocolSave is included when autoSave is enabled.
const engramMemoryProtocolSave = `
### WHEN TO SAVE (mandatory — not optional)

Call mem_save IMMEDIATELY after any of these:
- Bug fix completed
- Architecture or design decision made
- Non-obvious discovery about the codebase or infrastructure
- Configuration change or environment setup
- Pattern established (naming, structure, convention)
- User preference or constraint learned

Format for mem_save:
- type: bugfix | decision | architecture | discovery | pattern | config | preference | learning
- title: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList", "Chose Zustand over Redux")
- content: Structured as:
  What: One sentence — what was done
  Why: What motivated it (user request, bug, performance, etc.)
  Where: Files or paths affected
  Learned: Gotchas, edge cases, things that surprised you (omit if none)
- tags: Relevant keywords for search (optional but recommended)

This is NOT optional. If you complete significant work and don't save it,
the next conversation starts blind.

After completing any meaningful task, call mem_save before moving on.
Save early, save often — small focused memories are better than one giant dump at the end.
`

// engramMemoryProtocolSaveDisabled is included when autoSave is disabled.
const engramMemoryProtocolSaveDisabled = `
### SAVING MEMORIES

You must NOT call mem_save autonomously. Memory saving is managed by the user
through the console UI. Focus on the task at hand — the user will decide what
knowledge is worth persisting.
`

// engramMemoryProtocolSearch is included when autoSearch is enabled.
const engramMemoryProtocolSearch = `
### WHEN TO SEARCH MEMORY

Search memory AUTOMATICALLY (no need to ask) when the user references past work:
- Any variation of "remember", "recall", "what did we do", "how did we solve",
  "last time", "that bug", "we had", "before", or references to previous work
- Call mem_search with relevant keywords (FTS5 full-text search)

ASK BEFORE SEARCHING when it is your own idea during troubleshooting:
- You encounter an error or unexpected behavior and think prior knowledge may help
- Say something like: "Should I check if we've documented something about this?"
- Only search after the user confirms

DO NOT search memory on every conversation start. Do not call mem_context or
mem_search unless there is a specific reason. Casual greetings and simple
questions do not need memory lookups.

When the user asks "what have we done" or similar, search memory first — don't guess.
`

// engramMemoryProtocolSearchDisabled is included when autoSearch is disabled.
const engramMemoryProtocolSearchDisabled = `
### SEARCHING MEMORY

You must NOT call mem_search or mem_context autonomously. Memory search is
managed by the user through the console UI. If you think past knowledge might
be relevant, tell the user — they can look it up and share it with you.
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

	protocol := engramMemoryProtocolHeader

	if autoSave {
		protocol += engramMemoryProtocolSave
	} else {
		protocol += engramMemoryProtocolSaveDisabled
	}

	if autoSearch {
		protocol += engramMemoryProtocolSearch
	} else {
		protocol += engramMemoryProtocolSearchDisabled
	}

	return protocol
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

// ProviderEntry describes a configured provider.
type ProviderEntry struct {
	Name string `json:"name"`
}

// ToolHooksEntry holds runtime hook config.
type ToolHooksEntry struct {
	BlockedCommands []string `json:"blockedCommands,omitempty"`
	AllowedPaths    []string `json:"allowedPaths,omitempty"`
	AuditTools      []string `json:"auditTools,omitempty"`
}

// ContextEntry describes a context file.
type ContextEntry struct {
	Path string `json:"path"`
}

// AgentResourceEntry describes a resource binding for the runtime config.
type AgentResourceEntry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	AutoContext bool   `json:"autoContext,omitempty"`

	// Kind-specific config (one of these will be set)
	GitHub        *AgentResourceGitHubEntry        `json:"github,omitempty"`
	GitHubOrg     *AgentResourceGitHubOrgEntry     `json:"githubOrg,omitempty"`
	GitLab        *AgentResourceGitLabEntry        `json:"gitlab,omitempty"`
	GitLabGroup   *AgentResourceGitLabGroupEntry   `json:"gitlabGroup,omitempty"`
	Git           *AgentResourceGitEntry           `json:"git,omitempty"`
	S3            *AgentResourceS3Entry            `json:"s3,omitempty"`
	Documentation *AgentResourceDocumentationEntry `json:"documentation,omitempty"`
}

// AgentResourceGitHubEntry holds GitHub repo config for the runtime.
type AgentResourceGitHubEntry struct {
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	APIURL        string `json:"apiURL,omitempty"`
}

// AgentResourceGitHubOrgEntry holds GitHub org config for the runtime.
type AgentResourceGitHubOrgEntry struct {
	Org        string   `json:"org"`
	RepoFilter []string `json:"repoFilter,omitempty"`
	APIURL     string   `json:"apiURL,omitempty"`
}

// AgentResourceGitLabEntry holds GitLab project config for the runtime.
type AgentResourceGitLabEntry struct {
	BaseURL       string `json:"baseURL"`
	Project       string `json:"project"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

// AgentResourceGitLabGroupEntry holds GitLab group config for the runtime.
type AgentResourceGitLabGroupEntry struct {
	BaseURL  string   `json:"baseURL"`
	Group    string   `json:"group"`
	Projects []string `json:"projects,omitempty"`
}

// AgentResourceGitEntry holds plain git repo config for the runtime.
type AgentResourceGitEntry struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
}

// AgentResourceS3Entry holds S3 bucket config for the runtime.
type AgentResourceS3Entry struct {
	Bucket   string `json:"bucket"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// AgentResourceDocumentationEntry holds documentation config for the runtime.
type AgentResourceDocumentationEntry struct {
	URLs []string `json:"urls,omitempty"`
}

// MemoryConfigEntry holds memory configuration for the runtime.
type MemoryConfigEntry struct {
	ServerURL     string `json:"serverURL"`
	Project       string `json:"project"`
	ContextLimit  int    `json:"contextLimit"`
	WindowSize    int    `json:"windowSize"`
	AutoSummarize bool   `json:"autoSummarize"`
	AutoSave      bool   `json:"autoSave"`
	AutoSearch    bool   `json:"autoSearch"`
}

// AgentConfig is the JSON structure mounted at /etc/operator/config.json for the Fantasy runtime.
type AgentConfig struct {
	Runtime            string               `json:"runtime"`
	Providers          []ProviderEntry      `json:"providers"`
	PrimaryProvider    string               `json:"primaryProvider,omitempty"`
	PrimaryModel       string               `json:"primaryModel"`
	FallbackModels     []string             `json:"fallbackModels,omitempty"`
	TitleModel         string               `json:"titleModel,omitempty"`
	SystemPrompt       string               `json:"systemPrompt,omitempty"`
	BuiltinTools       []string             `json:"builtinTools,omitempty"`
	Tools              []ToolEntry          `json:"tools"`
	MCPServers         []MCPEntry           `json:"mcpServers,omitempty"`
	ToolHooks          *ToolHooksEntry      `json:"toolHooks,omitempty"`
	ContextFiles       []ContextEntry       `json:"contextFiles,omitempty"`
	Temperature        *float64             `json:"temperature,omitempty"`
	MaxOutputTokens    *int64               `json:"maxOutputTokens,omitempty"`
	MaxSteps           *int                 `json:"maxSteps,omitempty"`
	MaxToolResultChars int                  `json:"maxToolResultChars,omitempty"`
	BudgetFraction     *float64             `json:"budgetFraction,omitempty"`
	PermissionTools    []string             `json:"permissionTools,omitempty"`
	EnableQuestionTool bool                 `json:"enableQuestionTool,omitempty"`
	Resources          []AgentResourceEntry `json:"resources,omitempty"`
	Memory             *MemoryConfigEntry   `json:"memory,omitempty"`
}

// ====================================================================
// ConfigMap builder
// ====================================================================

// BuildAgentConfigMap generates the operator extension ConfigMap from an Agent spec.
// agentTools is the resolved list of AgentTool CRs (used to look up the memory server URL
// and to build tool/MCP entries).
func BuildAgentConfigMap(agent *agentsv1alpha1.Agent, agentResources []agentsv1alpha1.AgentResource, agentTools []agentsv1alpha1.AgentTool) (*corev1.ConfigMap, error) {
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
			windowSize := agent.Spec.Memory.WindowSize
			if windowSize == 0 {
				windowSize = 20
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
				WindowSize:    windowSize,
				AutoSummarize: autoSummarize,
				AutoSave:      autoSave,
				AutoSearch:    autoSearch,
			}

			// Append the Engram Memory Protocol to the system prompt
			// so the agent knows when to use mem_save/mem_search/mem_context.
			// The protocol sections are conditional based on autoSave/autoSearch.
			config.SystemPrompt = strings.TrimRight(config.SystemPrompt, "\n ") + buildMemoryProtocol(agent.Spec.Memory)
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

	// Providers
	for _, p := range agent.Spec.Providers {
		config.Providers = append(config.Providers, ProviderEntry{Name: p.Name})
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
	}

	// Resources (AgentResource bindings)
	bindingMap := make(map[string]agentsv1alpha1.AgentResourceBinding)
	for _, b := range agent.Spec.ResourceBindings {
		bindingMap[b.Name] = b
	}
	for _, res := range agentResources {
		binding := bindingMap[res.Name]
		entry := AgentResourceEntry{
			Name:        res.Name,
			Kind:        string(res.Spec.Kind),
			DisplayName: res.Spec.DisplayName,
			Description: res.Spec.Description,
			ReadOnly:    binding.ReadOnly,
			AutoContext: binding.AutoContext,
		}
		mapAgentResourceKind(&entry, &res)
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

// BuildAgentRunConfigMap creates a per-run ConfigMap that extends the base agent
// config with git MCP server entries. The runtime needs these to discover and
// connect to the MCP gateway sidecars injected by the operator.
func BuildAgentRunConfigMap(baseConfigMap *corev1.ConfigMap, runName string, gitMCPServers []MCPEntry) (*corev1.ConfigMap, error) {
	configJSON := baseConfigMap.Data["config.json"]
	var config AgentConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, fmt.Errorf("unmarshal base config: %w", err)
	}

	config.MCPServers = append(config.MCPServers, gitMCPServers...)

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

// mapAgentResourceKind populates the kind-specific fields of an AgentResourceEntry
// based on the AgentResource spec. Extracted to reduce cyclomatic complexity of
// BuildAgentConfigMap.
func mapAgentResourceKind(entry *AgentResourceEntry, res *agentsv1alpha1.AgentResource) {
	switch res.Spec.Kind {
	case agentsv1alpha1.AgentResourceKindGitHubRepo:
		if res.Spec.GitHub != nil {
			entry.GitHub = &AgentResourceGitHubEntry{
				Owner:         res.Spec.GitHub.Owner,
				Repo:          res.Spec.GitHub.Repo,
				DefaultBranch: res.Spec.GitHub.DefaultBranch,
				APIURL:        res.Spec.GitHub.APIURL,
			}
		}
	case agentsv1alpha1.AgentResourceKindGitHubOrg:
		if res.Spec.GitHubOrg != nil {
			entry.GitHubOrg = &AgentResourceGitHubOrgEntry{
				Org:        res.Spec.GitHubOrg.Org,
				RepoFilter: res.Spec.GitHubOrg.RepoFilter,
				APIURL:     res.Spec.GitHubOrg.APIURL,
			}
		}
	case agentsv1alpha1.AgentResourceKindGitLabProject:
		if res.Spec.GitLab != nil {
			entry.GitLab = &AgentResourceGitLabEntry{
				BaseURL:       res.Spec.GitLab.BaseURL,
				Project:       res.Spec.GitLab.Project,
				DefaultBranch: res.Spec.GitLab.DefaultBranch,
			}
		}
	case agentsv1alpha1.AgentResourceKindGitLabGroup:
		if res.Spec.GitLabGroup != nil {
			entry.GitLabGroup = &AgentResourceGitLabGroupEntry{
				BaseURL:  res.Spec.GitLabGroup.BaseURL,
				Group:    res.Spec.GitLabGroup.Group,
				Projects: res.Spec.GitLabGroup.Projects,
			}
		}
	case agentsv1alpha1.AgentResourceKindGitRepo:
		if res.Spec.Git != nil {
			entry.Git = &AgentResourceGitEntry{
				URL:    res.Spec.Git.URL,
				Branch: res.Spec.Git.Branch,
			}
		}
	case agentsv1alpha1.AgentResourceKindS3Bucket:
		if res.Spec.S3 != nil {
			entry.S3 = &AgentResourceS3Entry{
				Bucket:   res.Spec.S3.Bucket,
				Region:   res.Spec.S3.Region,
				Endpoint: res.Spec.S3.Endpoint,
				Prefix:   res.Spec.S3.Prefix,
			}
		}
	case agentsv1alpha1.AgentResourceKindDocumentation:
		if res.Spec.Documentation != nil {
			entry.Documentation = &AgentResourceDocumentationEntry{
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
