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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func boolPtr(b bool) *bool          { return &b }
func float64Ptr(f float64) *float64 { return &f }
func intPtr(i int) *int             { return &i }

func validPiAgent() *Agent {
	return &Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pi", Namespace: "default"},
		Spec: AgentSpec{
			Mode:  AgentModeDaemon,
			Model: "anthropic/claude-sonnet-4-20250514",
			Providers: []ProviderRef{
				{Name: "anthropic", ApiKeySecret: SecretKeyRef{Name: "keys", Key: "key"}},
			},
			Pi: &PiRuntimeConfig{
				BuiltinTools: []string{"read", "bash", "edit"},
			},
		},
	}
}

func validFantasyAgent() *Agent {
	return &Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "test-fantasy", Namespace: "default"},
		Spec: AgentSpec{
			Mode:  AgentModeDaemon,
			Model: "anthropic/claude-sonnet-4-20250514",
			Providers: []ProviderRef{
				{Name: "anthropic", ApiKeySecret: SecretKeyRef{Name: "keys", Key: "key"}},
			},
			Fantasy: &FantasyRuntimeConfig{
				BuiltinTools: []string{"bash", "read", "edit", "write"},
			},
		},
	}
}

// ── Runtime selection ──

func TestValidate_NoRuntime(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Pi = nil
	agent.Spec.Fantasy = nil

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error when no runtime is set")
	}
}

func TestValidate_BothRuntimes(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Fantasy = &FantasyRuntimeConfig{BuiltinTools: []string{"bash"}}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error when both runtimes are set")
	}
}

func TestValidate_ValidPi(t *testing.T) {
	agent := validPiAgent()
	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_ValidFantasy(t *testing.T) {
	agent := validFantasyAgent()
	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Pi builtin tools ──

func TestValidate_Pi_InvalidTool(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Pi.BuiltinTools = []string{"read", "bash", "glob"} // glob is Fantasy-only

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for invalid Pi tool 'glob'")
	}
}

func TestValidate_Pi_ValidTools(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Pi.BuiltinTools = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Fantasy builtin tools ──

func TestValidate_Fantasy_InvalidTool(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.BuiltinTools = []string{"bash", "find"} // find is Pi-only

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for invalid Fantasy tool 'find'")
	}
}

func TestValidate_Fantasy_ValidTools(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.BuiltinTools = []string{"bash", "read", "edit", "write", "grep", "ls", "glob", "fetch"}

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Fantasy temperature ──

func TestValidate_Fantasy_TemperatureOutOfRange(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.Temperature = float64Ptr(3.0)

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for temperature > 2.0")
	}
}

func TestValidate_Fantasy_NegativeTemperature(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.Temperature = float64Ptr(-0.5)

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for negative temperature")
	}
}

func TestValidate_Fantasy_ValidTemperature(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.Temperature = float64Ptr(0.7)

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Fantasy maxSteps ──

func TestValidate_Fantasy_ZeroMaxSteps(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.MaxSteps = intPtr(0)

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for maxSteps = 0")
	}
}

// ── Mode validation ──

func TestValidate_TaskMode_NoStorage(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Mode = AgentModeTask
	agent.Spec.Storage = &StorageSpec{Size: "10Gi"}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for storage in task mode")
	}
}

func TestValidate_TaskMode_NoCompaction(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Mode = AgentModeTask
	agent.Spec.Pi.Compaction = &CompactionSpec{Enabled: boolPtr(true)}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for compaction in task mode")
	}
}

// ── Providers ──

func TestValidate_NoProviders(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Providers = nil

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error when no providers configured")
	}
}

// ── Tools: at least one required ──

func TestValidate_NoTools(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Pi.BuiltinTools = nil
	agent.Spec.ToolRefs = nil

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error when no tools configured")
	}
}

func TestValidate_ToolRefsOnly(t *testing.T) {
	agent := validFantasyAgent()
	agent.Spec.Fantasy.BuiltinTools = nil
	agent.Spec.ToolRefs = []ResourceRef{
		{Name: "my-tool", OCIRef: &OCIRef{Ref: "ghcr.io/test/tool:1.0"}},
	}

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── FallbackModels ──

func TestValidate_FallbackModel_UnknownProvider(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.FallbackModels = []string{"openai/gpt-4o"}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for fallback model with unconfigured provider")
	}
}

func TestValidate_FallbackModel_ValidProvider(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Providers = append(agent.Spec.Providers,
		ProviderRef{Name: "openai", ApiKeySecret: SecretKeyRef{Name: "keys", Key: "key"}},
	)
	agent.Spec.FallbackModels = []string{"openai/gpt-4o"}

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Schedule ──

func TestValidate_Schedule_MissingPrompt(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.Schedule = "0 * * * *"
	agent.Spec.SchedulePrompt = ""

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error when schedule set without schedulePrompt")
	}
}

// ── ToolHooks ──

func TestValidate_ToolHooks_RelativePath(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.ToolHooks = &ToolHooksSpec{
		AllowedPaths: []string{"relative/path"},
	}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for relative path in allowedPaths")
	}
}

func TestValidate_ToolHooks_AbsolutePath(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.ToolHooks = &ToolHooksSpec{
		AllowedPaths: []string{"/data/workspace"},
	}

	_, err := agent.validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── ResourceRefs ──

func TestValidate_ToolRef_MultipleSourcesInvalid(t *testing.T) {
	agent := validPiAgent()
	agent.Spec.ToolRefs = []ResourceRef{
		{
			Name:         "bad-tool",
			OCIRef:       &OCIRef{Ref: "ghcr.io/test:1.0"},
			ConfigMapRef: &SecretKeyRef{Name: "cm", Key: "k"},
		},
	}

	_, err := agent.validate()
	if err == nil {
		t.Fatal("expected error for tool with multiple sources")
	}
}

// ── Helper methods ──

func TestRuntimeType(t *testing.T) {
	pi := validPiAgent()
	if pi.RuntimeType() != "pi" {
		t.Fatalf("expected 'pi', got %q", pi.RuntimeType())
	}

	fantasy := validFantasyAgent()
	if fantasy.RuntimeType() != "fantasy" {
		t.Fatalf("expected 'fantasy', got %q", fantasy.RuntimeType())
	}

	empty := &Agent{}
	if empty.RuntimeType() != "" {
		t.Fatalf("expected empty string, got %q", empty.RuntimeType())
	}
}

func TestRuntimeImage_Defaults(t *testing.T) {
	pi := validPiAgent()
	if pi.RuntimeImage() != DefaultPiImage {
		t.Fatalf("expected %q, got %q", DefaultPiImage, pi.RuntimeImage())
	}

	fantasy := validFantasyAgent()
	if fantasy.RuntimeImage() != DefaultFantasyImage {
		t.Fatalf("expected %q, got %q", DefaultFantasyImage, fantasy.RuntimeImage())
	}
}

func TestRuntimeImage_Custom(t *testing.T) {
	pi := validPiAgent()
	pi.Spec.Pi.Image = "custom:1.0"
	if pi.RuntimeImage() != "custom:1.0" {
		t.Fatalf("expected 'custom:1.0', got %q", pi.RuntimeImage())
	}
}

func TestBuiltinToolCount(t *testing.T) {
	pi := validPiAgent()
	if pi.BuiltinToolCount() != 3 {
		t.Fatalf("expected 3, got %d", pi.BuiltinToolCount())
	}

	fantasy := validFantasyAgent()
	if fantasy.BuiltinToolCount() != 4 {
		t.Fatalf("expected 4, got %d", fantasy.BuiltinToolCount())
	}
}
