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
	"context"
	"fmt"
	"path/filepath"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var agentlog = logf.Log.WithName("agent-webhook")

// Valid built-in tool names per runtime.
var piBuiltinTools = map[string]bool{
	"read": true, "bash": true, "edit": true, "write": true,
	"grep": true, "find": true, "ls": true,
}

var fantasyBuiltinTools = map[string]bool{
	"bash": true, "read": true, "edit": true, "write": true,
	"grep": true, "ls": true, "glob": true, "fetch": true,
}

// SetupAgentWebhookWithManager registers the Agent validating webhook.
func (r *Agent) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agenticops-io-v1alpha1-agent,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agenticops.io,resources=agents,verbs=create;update,versions=v1alpha1,name=vagent.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &Agent{}

// ValidateCreate implements webhook.CustomValidator.
func (r *Agent) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	agentlog.Info("validate create", "name", r.Name)
	agent := obj.(*Agent)
	return agent.validate()
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *Agent) ValidateUpdate(_ context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	agentlog.Info("validate update", "name", r.Name)
	agent := newObj.(*Agent)
	return agent.validate()
}

// ValidateDelete implements webhook.CustomValidator.
func (r *Agent) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (r *Agent) validate() (admission.Warnings, error) {
	allErrs := make(field.ErrorList, 0, 8)
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, r.validateMode(specPath)...)
	allErrs = append(allErrs, r.validateRuntime(specPath)...)
	allErrs = append(allErrs, r.validateProviders(specPath)...)
	allErrs = append(allErrs, r.validateTools(specPath)...)
	errs, warnings := r.validateToolHooks(specPath)
	allErrs = append(allErrs, errs...)
	allErrs = append(allErrs, r.validateResourceRefs(specPath)...)
	allErrs = append(allErrs, r.validateSchedule(specPath)...)

	if len(allErrs) > 0 {
		return warnings, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Agent"},
			r.Name, allErrs)
	}

	return warnings, nil
}

func (r *Agent) validateMode(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	if r.Spec.Mode != AgentModeDaemon && r.Spec.Mode != AgentModeTask {
		errs = append(errs, field.Invalid(specPath.Child("mode"), r.Spec.Mode,
			"must be 'daemon' or 'task'"))
	}

	if r.Spec.Mode == AgentModeTask {
		if r.Spec.Storage != nil {
			errs = append(errs, field.Forbidden(specPath.Child("storage"),
				"storage is not allowed for task-mode agents"))
		}
		// Pi compaction is daemon-only
		if r.Spec.Pi != nil && r.Spec.Pi.Compaction != nil {
			errs = append(errs, field.Forbidden(specPath.Child("pi").Child("compaction"),
				"compaction is daemon-only"))
		}
	}

	return errs
}

// validateRuntime ensures exactly one runtime block is set and validates its contents.
func (r *Agent) validateRuntime(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	runtimeCount := 0
	if r.Spec.Pi != nil {
		runtimeCount++
	}
	if r.Spec.Fantasy != nil {
		runtimeCount++
	}

	if runtimeCount == 0 {
		errs = append(errs, field.Required(specPath,
			"exactly one runtime must be specified: set either 'pi' or 'fantasy'"))
		return errs
	}
	if runtimeCount > 1 {
		errs = append(errs, field.Forbidden(specPath,
			"only one runtime can be specified: 'pi' and 'fantasy' are mutually exclusive"))
		return errs
	}

	// Runtime-specific validation
	if r.Spec.Pi != nil {
		errs = append(errs, r.validatePiRuntime(specPath.Child("pi"))...)
	}
	if r.Spec.Fantasy != nil {
		errs = append(errs, r.validateFantasyRuntime(specPath.Child("fantasy"))...)
	}

	return errs
}

func (r *Agent) validatePiRuntime(piPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// Validate builtin tool names
	for i, tool := range r.Spec.Pi.BuiltinTools {
		if !piBuiltinTools[tool] {
			errs = append(errs, field.Invalid(
				piPath.Child("builtinTools").Index(i), tool,
				"valid Pi tools: read, bash, edit, write, grep, find, ls"))
		}
	}

	// Compaction strategy validation
	if r.Spec.Pi.Compaction != nil {
		validStrategies := map[string]bool{"auto": true, "manual": true, "off": true, "": true}
		if !validStrategies[r.Spec.Pi.Compaction.Strategy] {
			errs = append(errs, field.Invalid(
				piPath.Child("compaction").Child("strategy"), r.Spec.Pi.Compaction.Strategy,
				"must be auto, manual, or off"))
		}
	}

	return errs
}

func (r *Agent) validateFantasyRuntime(fantasyPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// Validate builtin tool names
	for i, tool := range r.Spec.Fantasy.BuiltinTools {
		if !fantasyBuiltinTools[tool] {
			errs = append(errs, field.Invalid(
				fantasyPath.Child("builtinTools").Index(i), tool,
				"valid Fantasy tools: bash, read, edit, write, grep, ls, glob, fetch"))
		}
	}

	// Temperature range
	if r.Spec.Fantasy.Temperature != nil {
		t := *r.Spec.Fantasy.Temperature
		if t < 0.0 || t > 2.0 {
			errs = append(errs, field.Invalid(
				fantasyPath.Child("temperature"), t,
				"must be between 0.0 and 2.0"))
		}
	}

	// MaxSteps > 0
	if r.Spec.Fantasy.MaxSteps != nil && *r.Spec.Fantasy.MaxSteps <= 0 {
		errs = append(errs, field.Invalid(
			fantasyPath.Child("maxSteps"), *r.Spec.Fantasy.MaxSteps,
			"must be > 0"))
	}

	return errs
}

func (r *Agent) validateProviders(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	if len(r.Spec.Providers) == 0 {
		errs = append(errs, field.Required(specPath.Child("providers"),
			"all agents need at least one LLM provider"))
	}

	// FallbackModels must reference configured providers
	providerNames := make(map[string]bool)
	for _, p := range r.Spec.Providers {
		providerNames[p.Name] = true
	}
	for i, model := range r.Spec.FallbackModels {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 {
			if !providerNames[parts[0]] {
				errs = append(errs, field.Invalid(
					specPath.Child("fallbackModels").Index(i), model,
					fmt.Sprintf("provider %q is not configured in providers", parts[0])))
			}
		}
	}

	return errs
}

func (r *Agent) validateTools(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// At least one tool source (builtin from active runtime OR toolRefs)
	toolCount := len(r.Spec.ToolRefs)
	toolCount += r.BuiltinToolCount()

	if toolCount == 0 {
		errs = append(errs, field.Required(specPath.Child("toolRefs"),
			"agents need at least one tool (builtinTools in runtime config or toolRefs)"))
	}

	return errs
}

func (r *Agent) validateSchedule(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	if r.Spec.Schedule != "" && r.Spec.SchedulePrompt == "" {
		errs = append(errs, field.Required(specPath.Child("schedulePrompt"),
			"schedulePrompt is required when schedule is set"))
	}

	return errs
}

func (r *Agent) validateToolHooks(specPath *field.Path) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings

	if r.Spec.ToolHooks == nil {
		return errs, warnings
	}

	// allowedPaths must be absolute
	for i, p := range r.Spec.ToolHooks.AllowedPaths {
		if !filepath.IsAbs(p) {
			errs = append(errs, field.Invalid(
				specPath.Child("toolHooks").Child("allowedPaths").Index(i), p,
				"must be an absolute path"))
		}
	}

	// Warn (non-blocking) if auditTools references unknown tools
	knownTools := make(map[string]bool)
	if r.Spec.Pi != nil {
		for _, bt := range r.Spec.Pi.BuiltinTools {
			knownTools[bt] = true
		}
	}
	if r.Spec.Fantasy != nil {
		for _, bt := range r.Spec.Fantasy.BuiltinTools {
			knownTools[bt] = true
		}
	}
	for _, tr := range r.Spec.ToolRefs {
		knownTools[tr.Name] = true
	}
	knownTools["run_agent"] = true
	knownTools["get_agent_run"] = true
	for _, at := range r.Spec.ToolHooks.AuditTools {
		if !knownTools[at] {
			warnings = append(warnings,
				fmt.Sprintf("toolHooks.auditTools: %q is not a known tool name", at))
		}
	}

	return errs, warnings
}

func (r *Agent) validateResourceRefs(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	for i, tr := range r.Spec.ToolRefs {
		sources := 0
		if tr.OCIRef != nil {
			sources++
		}
		if tr.ConfigMapRef != nil {
			sources++
		}
		if tr.Content != "" {
			sources++
		}
		if sources != 1 {
			errs = append(errs, field.Invalid(
				specPath.Child("toolRefs").Index(i), tr.Name,
				"exactly one of ociRef, configMapRef, or content must be set"))
		}
	}

	// Pi-specific resource refs
	if r.Spec.Pi != nil {
		for i, sk := range r.Spec.Pi.Skills {
			sources := 0
			if sk.OCIRef != nil {
				sources++
			}
			if sk.ConfigMapRef != nil {
				sources++
			}
			if sk.Content != "" {
				sources++
			}
			if sources != 1 {
				errs = append(errs, field.Invalid(
					specPath.Child("pi").Child("skills").Index(i), sk.Name,
					"exactly one of ociRef, configMapRef, or content must be set"))
			}
		}
	}

	return errs
}
