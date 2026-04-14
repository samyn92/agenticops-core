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
	"strings"
	"testing"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testSystemPrompt = "You are helpful."

func testAgent() *agentsv1alpha1.Agent {
	temp := 0.5
	maxTokens := int64(4096)
	maxSteps := 50
	return &agentsv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "test-agent", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentSpec{
			Mode:  agentsv1alpha1.AgentModeDaemon,
			Model: "anthropic/claude-sonnet-4-20250514",
			ProviderRefs: []agentsv1alpha1.ProviderBinding{
				{Name: "anthropic"},
			},
			BuiltinTools:    []string{"bash", "read", "edit", "write"},
			Temperature:     &temp,
			MaxOutputTokens: &maxTokens,
			MaxSteps:        &maxSteps,
			SystemPrompt:    testSystemPrompt,
		},
	}
}

// ── ConfigMap tests ──

func TestBuildAgentConfigMap(t *testing.T) {
	agent := testAgent()
	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.Data["config.json"]
	var cfg AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Runtime != "fantasy" {
		t.Errorf("expected runtime=fantasy, got %q", cfg.Runtime)
	}
	if cfg.PrimaryModel != "anthropic/claude-sonnet-4-20250514" {
		t.Errorf("unexpected model: %s", cfg.PrimaryModel)
	}
	if len(cfg.BuiltinTools) != 4 {
		t.Errorf("expected 4 builtinTools, got %d", len(cfg.BuiltinTools))
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.5 {
		t.Errorf("unexpected temperature: %v", cfg.Temperature)
	}
	if cfg.MaxOutputTokens == nil || *cfg.MaxOutputTokens != 4096 {
		t.Errorf("unexpected maxOutputTokens: %v", cfg.MaxOutputTokens)
	}
	if cfg.MaxSteps == nil || *cfg.MaxSteps != 50 {
		t.Errorf("unexpected maxSteps: %v", cfg.MaxSteps)
	}
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("unexpected systemPrompt: %q", cfg.SystemPrompt)
	}
}

// ── Deployment tests ──

func TestBuildAgentDeployment(t *testing.T) {
	agent := testAgent()
	deploy := BuildAgentDeployment(agent, nil, nil)

	containers := deploy.Spec.Template.Spec.Containers
	if len(containers) < 1 {
		t.Fatal("expected at least 1 container")
	}

	main := containers[0]
	if main.Name != ContainerRuntime {
		t.Errorf("expected container name %q, got %q", ContainerRuntime, main.Name)
	}
	if main.Image != agentsv1alpha1.DefaultFantasyImage {
		t.Errorf("expected image %q, got %q", agentsv1alpha1.DefaultFantasyImage, main.Image)
	}
	if main.Command[0] != "/app/agent-runtime" {
		t.Errorf("expected '/app/agent-runtime' command, got %q", main.Command[0])
	}
	if len(main.Command) < 2 || main.Command[1] != "daemon" {
		t.Errorf("expected 'daemon' arg, got %v", main.Command)
	}
}

func TestBuildAgentDeployment_EnvVars(t *testing.T) {
	agent := testAgent()
	deploy := BuildAgentDeployment(agent, nil, nil)

	main := deploy.Spec.Template.Spec.Containers[0]
	envMap := make(map[string]string)
	for _, e := range main.Env {
		if e.Value != "" {
			envMap[e.Name] = e.Value
		}
	}

	if envMap["AGENT_RUNTIME"] != "fantasy" {
		t.Errorf("expected AGENT_RUNTIME=fantasy, got %q", envMap["AGENT_RUNTIME"])
	}
	if envMap["AGENT_NAME"] != "test-agent" {
		t.Errorf("expected AGENT_NAME=test-agent, got %q", envMap["AGENT_NAME"])
	}
	if envMap["AGENT_MODE"] != "daemon" {
		t.Errorf("expected AGENT_MODE=daemon, got %q", envMap["AGENT_MODE"])
	}
}

func TestBuildAgentDeployment_CustomImage(t *testing.T) {
	agent := testAgent()
	agent.Spec.Image = "custom-registry.io/my-agent:v2"
	deploy := BuildAgentDeployment(agent, nil, nil)

	if deploy.Spec.Template.Spec.Containers[0].Image != "custom-registry.io/my-agent:v2" {
		t.Errorf("expected custom image, got %q", deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestBuildAgentDeployment_HealthCheck(t *testing.T) {
	agent := testAgent()
	deploy := BuildAgentDeployment(agent, nil, nil)

	main := deploy.Spec.Template.Spec.Containers[0]
	if main.LivenessProbe == nil {
		t.Fatal("expected liveness probe for daemon mode")
	}
	if main.ReadinessProbe == nil {
		t.Fatal("expected readiness probe for daemon mode")
	}
	if main.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Errorf("expected /healthz path, got %q", main.LivenessProbe.HTTPGet.Path)
	}
}

// ── Job tests ──

func TestBuildAgentRunJob(t *testing.T) {
	agent := testAgent()
	agent.Spec.Mode = agentsv1alpha1.AgentModeTask

	run := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "test-run", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentRunSpec{
			AgentRef: "test-agent",
			Prompt:   "Do something",
		},
	}

	job := BuildAgentRunJob(run, agent, nil, nil, nil, "")

	main := job.Spec.Template.Spec.Containers[0]
	if main.Command[0] != "/app/agent-runtime" || main.Command[1] != "task" {
		t.Errorf("expected task command, got %v", main.Command)
	}

	// Check AGENT_PROMPT is injected
	hasPrompt := false
	for _, e := range main.Env {
		if e.Name == "AGENT_PROMPT" && e.Value == "Do something" {
			hasPrompt = true
		}
	}
	if !hasPrompt {
		t.Error("expected AGENT_PROMPT env var in job")
	}

	// RestartPolicy should be Never
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Errorf("expected RestartPolicy=Never, got %q", job.Spec.Template.Spec.RestartPolicy)
	}
}

// ── MCP ConfigMap tests ──

func TestBuildMCPConfigMap_NoMCPTools(t *testing.T) {
	agent := testAgent()
	// No MCP-source tools
	cm, err := BuildMCPConfigMap(agent, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm != nil {
		t.Error("expected nil ConfigMap when no MCP tools")
	}
}

func TestBuildMCPConfigMap_WithMCPTools(t *testing.T) {
	agent := testAgent()
	agent.Spec.Tools = []agentsv1alpha1.AgentToolBinding{
		{Name: "github-mcp"},
	}

	agentTools := []agentsv1alpha1.AgentTool{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "github-mcp", Namespace: "agents"},
			Spec: agentsv1alpha1.AgentToolSpec{
				MCPServer: &agentsv1alpha1.MCPServerToolSource{
					Image: "ghcr.io/test/github-mcp:latest",
					Port:  8080,
				},
			},
			Status: agentsv1alpha1.AgentToolStatus{
				Phase:      agentsv1alpha1.AgentToolPhaseReady,
				SourceType: "mcpServer",
				ServiceURL: "http://agtool-github-mcp.agents.svc:8080",
			},
		},
	}

	cm, err := BuildMCPConfigMap(agent, agentTools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cm == nil {
		t.Fatal("expected ConfigMap")
	}

	data := cm.Data["mcp.json"]
	var mcpCfg MCPJsonConfig
	if err := json.Unmarshal([]byte(data), &mcpCfg); err != nil {
		t.Fatalf("failed to parse mcp.json: %v", err)
	}

	entry, ok := mcpCfg.MCPServers["github-mcp"]
	if !ok {
		t.Fatal("expected github-mcp in mcp.json")
	}
	if entry.Type != "sse" {
		t.Errorf("expected type=sse, got %q", entry.Type)
	}
	if !strings.Contains(entry.URL, "9001") {
		t.Errorf("expected port 9001 in URL, got %q", entry.URL)
	}
}

// ── AgentResource ConfigMap tests ──

func TestBuildAgentConfigMap_WithResources(t *testing.T) {
	agent := testAgent()
	agent.Spec.ResourceBindings = []agentsv1alpha1.AgentResourceBinding{
		{Name: "my-repo", ReadOnly: true, AutoContext: true},
		{Name: "my-group"},
	}

	resources := []agentsv1alpha1.AgentResource{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "agents"},
			Spec: agentsv1alpha1.AgentResourceSpec{
				Kind:        agentsv1alpha1.AgentResourceKindGitHubRepo,
				DisplayName: "My Repo",
				Description: "A test repo",
				GitHub: &agentsv1alpha1.GitHubResourceConfig{
					Owner:         "samyn92",
					Repo:          "agentops-core",
					DefaultBranch: "main",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "my-group", Namespace: "agents"},
			Spec: agentsv1alpha1.AgentResourceSpec{
				Kind:        agentsv1alpha1.AgentResourceKindGitLabGroup,
				DisplayName: "My Group",
				GitLabGroup: &agentsv1alpha1.GitLabGroupResourceConfig{
					BaseURL:  "https://gitlab.com",
					Group:    "homecluster",
					Projects: []string{"flux"},
				},
			},
		},
	}

	cm, err := BuildAgentConfigMap(agent, resources, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.Data["config.json"]
	var cfg AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if len(cfg.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(cfg.Resources))
	}

	// First resource: github-repo
	r1 := cfg.Resources[0]
	if r1.Name != "my-repo" {
		t.Errorf("expected name=my-repo, got %q", r1.Name)
	}
	if r1.Kind != "github-repo" {
		t.Errorf("expected kind=github-repo, got %q", r1.Kind)
	}
	if r1.DisplayName != "My Repo" {
		t.Errorf("expected displayName=My Repo, got %q", r1.DisplayName)
	}
	if !r1.ReadOnly {
		t.Error("expected readOnly=true")
	}
	if !r1.AutoContext {
		t.Error("expected autoContext=true")
	}
	if r1.GitHub == nil {
		t.Fatal("expected github config")
	}
	if r1.GitHub.Owner != "samyn92" {
		t.Errorf("expected owner=samyn92, got %q", r1.GitHub.Owner)
	}
	if r1.GitHub.Repo != "agentops-core" {
		t.Errorf("expected repo=agentops-core, got %q", r1.GitHub.Repo)
	}

	// Second resource: gitlab-group
	r2 := cfg.Resources[1]
	if r2.Kind != "gitlab-group" {
		t.Errorf("expected kind=gitlab-group, got %q", r2.Kind)
	}
	if r2.ReadOnly {
		t.Error("expected readOnly=false")
	}
	if r2.GitLabGroup == nil {
		t.Fatal("expected gitlabGroup config")
	}
	if r2.GitLabGroup.Group != "homecluster" {
		t.Errorf("expected group=homecluster, got %q", r2.GitLabGroup.Group)
	}
}

func TestBuildAgentConfigMap_NoResources(t *testing.T) {
	agent := testAgent()
	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.Data["config.json"]
	var cfg AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if len(cfg.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(cfg.Resources))
	}
}

// ── Memory Protocol tests ──

func TestBuildAgentConfigMap_MemoryProtocolInPlatformProtocol(t *testing.T) {
	agent := testAgent()
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{
		ServerRef: "engram",
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.Data["config.json"]
	var cfg AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Memory config should be set
	if cfg.Memory == nil {
		t.Fatal("expected memory config")
	}
	if cfg.Memory.Project != "test-agent" {
		t.Errorf("expected project=test-agent, got %q", cfg.Memory.Project)
	}

	// System prompt should be UNMODIFIED — no memory protocol appended
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}

	// Platform protocol should contain identity + memory protocol
	if cfg.PlatformProtocol == "" {
		t.Fatal("expected platformProtocol to be set")
	}
	if !strings.Contains(cfg.PlatformProtocol, "You are test-agent, a daemon agent") {
		t.Error("expected agent identity in platform protocol")
	}
	if !strings.Contains(cfg.PlatformProtocol, "## Memory System") {
		t.Error("expected Memory System header in platform protocol")
	}
	if !strings.Contains(cfg.PlatformProtocol, "mem_save") {
		t.Error("expected platform protocol to reference mem_save")
	}
	if !strings.Contains(cfg.PlatformProtocol, "mem_search") {
		t.Error("expected platform protocol to reference mem_search")
	}
}

func TestBuildAgentConfigMap_NoMemoryNoProtocol(t *testing.T) {
	agent := testAgent()
	// No memory spec set

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.Data["config.json"]
	var cfg AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// System prompt should NOT contain memory protocol
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}
	// Platform protocol should contain identity but NOT memory protocol
	if strings.Contains(cfg.PlatformProtocol, "## Memory System") {
		t.Error("expected platform protocol to NOT contain Memory System header when memory is disabled")
	}
}

// ── Memory autonomy mode tests ──

func TestBuildAgentConfigMap_MemoryFullAutonomy(t *testing.T) {
	// autoSave=true, autoSearch=true (defaults)
	agent := testAgent()
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{ServerRef: "engram"}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if !cfg.Memory.AutoSave {
		t.Error("expected autoSave=true")
	}
	if !cfg.Memory.AutoSearch {
		t.Error("expected autoSearch=true")
	}
	if !strings.Contains(cfg.PlatformProtocol, "call mem_save after") {
		t.Error("expected save instructions in platform protocol")
	}
	if !strings.Contains(cfg.PlatformProtocol, "call mem_search when") {
		t.Error("expected search instructions in platform protocol")
	}
	if strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_save") {
		t.Error("should not contain save-disabled instructions")
	}
	if strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_search") {
		t.Error("should not contain search-disabled instructions")
	}
	// SystemPrompt should be untouched
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}
}

func TestBuildAgentConfigMap_MemoryNoAutoSave(t *testing.T) {
	// autoSave=false, autoSearch=true
	agent := testAgent()
	f := false
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{
		ServerRef: "engram",
		AutoSave:  &f,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Memory.AutoSave {
		t.Error("expected autoSave=false")
	}
	if !cfg.Memory.AutoSearch {
		t.Error("expected autoSearch=true")
	}
	if strings.Contains(cfg.PlatformProtocol, "call mem_save after") {
		t.Error("should not contain save instructions when autoSave=false")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_save") {
		t.Error("expected save-disabled instructions")
	}
	if !strings.Contains(cfg.PlatformProtocol, "call mem_search when") {
		t.Error("expected search instructions")
	}
}

func TestBuildAgentConfigMap_MemoryNoAutoSearch(t *testing.T) {
	// autoSave=true, autoSearch=false
	agent := testAgent()
	f := false
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{
		ServerRef:  "engram",
		AutoSearch: &f,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if !cfg.Memory.AutoSave {
		t.Error("expected autoSave=true")
	}
	if cfg.Memory.AutoSearch {
		t.Error("expected autoSearch=false")
	}
	if !strings.Contains(cfg.PlatformProtocol, "call mem_save after") {
		t.Error("expected save instructions")
	}
	if strings.Contains(cfg.PlatformProtocol, "call mem_search when") {
		t.Error("should not contain search instructions when autoSearch=false")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_search") {
		t.Error("expected search-disabled instructions")
	}
}

func TestBuildAgentConfigMap_MemoryFullyPassive(t *testing.T) {
	// autoSave=false, autoSearch=false
	agent := testAgent()
	f := false
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{
		ServerRef:  "engram",
		AutoSave:   &f,
		AutoSearch: &f,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if cfg.Memory.AutoSave {
		t.Error("expected autoSave=false")
	}
	if cfg.Memory.AutoSearch {
		t.Error("expected autoSearch=false")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_save") {
		t.Error("expected save-disabled instructions")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Do NOT call mem_search") {
		t.Error("expected search-disabled instructions")
	}
	if strings.Contains(cfg.PlatformProtocol, "call mem_save after") {
		t.Error("should not contain save instructions")
	}
	if strings.Contains(cfg.PlatformProtocol, "call mem_search when") {
		t.Error("should not contain search instructions")
	}
	// Should still have the header in platform protocol
	if !strings.Contains(cfg.PlatformProtocol, "## Memory System") {
		t.Error("expected memory protocol header even in passive mode")
	}
	// SystemPrompt should be untouched
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}
}

// ── Delegation Protocol tests ──

func TestBuildAgentConfigMap_DelegationProactive(t *testing.T) {
	agent := testAgent()
	agent.Spec.Delegation = &agentsv1alpha1.DelegationSpec{
		Strategy:       agentsv1alpha1.DelegationStrategyProactive,
		PreferParallel: true,
		MaxFanOut:      5,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Platform protocol should contain delegation protocol
	if !strings.Contains(cfg.PlatformProtocol, "## Delegation") {
		t.Error("expected delegation header in platform protocol")
	}
	if !strings.Contains(cfg.PlatformProtocol, "delegate immediately") {
		t.Error("expected proactive delegation instructions")
	}
	if !strings.Contains(cfg.PlatformProtocol, "run_agents to execute them in parallel") {
		t.Error("expected parallel delegation instructions")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Maximum parallel delegations per fan-out: 5") {
		t.Error("expected maxFanOut in platform protocol")
	}

	// SystemPrompt should be untouched
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}

	// Delegation config should be set for runtime enforcement
	if cfg.Delegation == nil {
		t.Fatal("expected delegation config")
	}
	if cfg.Delegation.Strategy != "proactive" {
		t.Errorf("expected strategy=proactive, got %q", cfg.Delegation.Strategy)
	}
	if !cfg.Delegation.PreferParallel {
		t.Error("expected preferParallel=true")
	}
	if cfg.Delegation.MaxFanOut != 5 {
		t.Errorf("expected maxFanOut=5, got %d", cfg.Delegation.MaxFanOut)
	}
}

func TestBuildAgentConfigMap_DelegationConservative(t *testing.T) {
	agent := testAgent()
	agent.Spec.Delegation = &agentsv1alpha1.DelegationSpec{
		Strategy: agentsv1alpha1.DelegationStrategyConservative,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if !strings.Contains(cfg.PlatformProtocol, "Attempt tasks yourself first") {
		t.Error("expected conservative delegation instructions")
	}
	if !strings.Contains(cfg.PlatformProtocol, "Delegate tasks one at a time") {
		t.Error("expected sequential delegation instructions (preferParallel=false)")
	}
}

func TestBuildAgentConfigMap_DelegationManual(t *testing.T) {
	agent := testAgent()
	agent.Spec.Delegation = &agentsv1alpha1.DelegationSpec{
		Strategy: agentsv1alpha1.DelegationStrategyManual,
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if !strings.Contains(cfg.PlatformProtocol, "do NOT delegate tasks unless the user explicitly asks") {
		t.Error("expected manual delegation instructions")
	}
}

func TestBuildAgentConfigMap_NoDelegationSpec(t *testing.T) {
	agent := testAgent()
	// No delegation spec

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if strings.Contains(cfg.PlatformProtocol, "## Delegation") {
		t.Error("expected no delegation protocol when delegation spec is unset")
	}
	if cfg.Delegation != nil {
		t.Error("expected nil delegation config when spec is unset")
	}
}

// ── Platform Protocol identity tests ──

func TestBuildAgentConfigMap_PlatformProtocolIdentity(t *testing.T) {
	agent := testAgent()
	agent.Spec.Discovery = &agentsv1alpha1.DiscoverySpec{
		Description: "A helpful test agent for unit tests.",
	}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	if !strings.Contains(cfg.PlatformProtocol, "You are test-agent, a daemon agent in the agents namespace.") {
		t.Errorf("expected agent identity in platform protocol, got %q", cfg.PlatformProtocol)
	}
	if !strings.Contains(cfg.PlatformProtocol, "A helpful test agent for unit tests.") {
		t.Error("expected discovery description in platform protocol identity")
	}
}

// ── Combined delegation + memory tests ──

func TestBuildAgentConfigMap_DelegationAndMemory(t *testing.T) {
	agent := testAgent()
	agent.Spec.Delegation = &agentsv1alpha1.DelegationSpec{
		Strategy:       agentsv1alpha1.DelegationStrategyProactive,
		PreferParallel: true,
		MaxFanOut:      3,
	}
	agent.Spec.Memory = &agentsv1alpha1.MemorySpec{ServerRef: "engram"}

	cm, err := BuildAgentConfigMap(agent, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Platform protocol should contain both delegation and memory
	if !strings.Contains(cfg.PlatformProtocol, "## Delegation") {
		t.Error("expected delegation header")
	}
	if !strings.Contains(cfg.PlatformProtocol, "## Memory System") {
		t.Error("expected memory header")
	}
	if !strings.Contains(cfg.PlatformProtocol, "You are test-agent") {
		t.Error("expected agent identity")
	}

	// SystemPrompt untouched
	if cfg.SystemPrompt != testSystemPrompt {
		t.Errorf("expected unmodified system prompt, got %q", cfg.SystemPrompt)
	}
}
