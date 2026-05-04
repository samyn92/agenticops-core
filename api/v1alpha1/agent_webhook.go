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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var agentlog = logf.Log.WithName("agent-webhook")

// Valid built-in tool names for the Fantasy runtime.
var builtinTools = map[string]bool{
	"bash": true, "read": true, "edit": true, "write": true,
	"grep": true, "ls": true, "glob": true, "fetch": true,
}

// SetupAgentWebhookWithManager registers the Agent validating webhook.
func (r *Agent) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-agent,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=agents,verbs=create;update,versions=v1alpha1,name=vagent.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*Agent] = &Agent{}

// ValidateCreate implements admission.Validator.
func (r *Agent) ValidateCreate(_ context.Context, obj *Agent) (admission.Warnings, error) {
	agentlog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *Agent) ValidateUpdate(_ context.Context, _ *Agent, newObj *Agent) (admission.Warnings, error) {
	agentlog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *Agent) ValidateDelete(_ context.Context, _ *Agent) (admission.Warnings, error) {
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
	allErrs = append(allErrs, r.validateResourceBindings(specPath)...)
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
	}

	// Validate storage size when storage is configured
	if r.Spec.Storage != nil && r.Spec.Storage.Size == "" {
		errs = append(errs, field.Required(specPath.Child("storage", "size"),
			"storage size is required when storage is configured (e.g. \"1Gi\")"))
	}

	return errs
}

// validateRuntime validates the runtime configuration fields.
func (r *Agent) validateRuntime(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// Validate builtin tool names
	for i, tool := range r.Spec.BuiltinTools {
		if !builtinTools[tool] {
			errs = append(errs, field.Invalid(
				specPath.Child("builtinTools").Index(i), tool,
				"valid tools: bash, read, edit, write, grep, ls, glob, fetch"))
		}
	}

	// Temperature range
	if r.Spec.Temperature != nil {
		t := *r.Spec.Temperature
		if t < 0.0 || t > 2.0 {
			errs = append(errs, field.Invalid(
				specPath.Child("temperature"), t,
				"must be between 0.0 and 2.0"))
		}
	}

	// MaxSteps > 0
	if r.Spec.MaxSteps != nil && *r.Spec.MaxSteps <= 0 {
		errs = append(errs, field.Invalid(
			specPath.Child("maxSteps"), *r.Spec.MaxSteps,
			"must be > 0"))
	}

	return errs
}

func (r *Agent) validateProviders(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// providerRefs must be non-empty (enforced by kubebuilder MinItems=1, but belt-and-suspenders)
	if len(r.Spec.ProviderRefs) == 0 {
		errs = append(errs, field.Required(specPath.Child("providerRefs"),
			"at least one Provider CR reference is required"))
	}

	// Build set of all known provider names
	providerNames := make(map[string]bool)
	for _, ref := range r.Spec.ProviderRefs {
		providerNames[ref.Name] = true
	}

	// FallbackModels must reference configured providers
	for i, model := range r.Spec.FallbackModels {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 {
			if !providerNames[parts[0]] {
				errs = append(errs, field.Invalid(
					specPath.Child("fallbackModels").Index(i), model,
					fmt.Sprintf("provider %q is not configured in providerRefs", parts[0])))
			}
		}
	}

	return errs
}

func (r *Agent) validateTools(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// At least one tool source (builtin OR tools)
	toolCount := len(r.Spec.Tools)
	toolCount += r.BuiltinToolCount()

	if toolCount == 0 {
		errs = append(errs, field.Required(specPath.Child("tools"),
			"agents need at least one tool (builtinTools or tools)"))
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
	for _, bt := range r.Spec.BuiltinTools {
		knownTools[bt] = true
	}
	for _, tb := range r.Spec.Tools {
		knownTools[tb.Name] = true
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

func (r *Agent) validateResourceBindings(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// Check for duplicate resource binding names
	seen := make(map[string]bool)
	for i, rb := range r.Spec.ResourceBindings {
		if rb.Name == "" {
			errs = append(errs, field.Required(
				specPath.Child("resourceBindings").Index(i).Child("name"),
				"resource binding name is required"))
			continue
		}
		if seen[rb.Name] {
			errs = append(errs, field.Invalid(
				specPath.Child("resourceBindings").Index(i).Child("name"), rb.Name,
				"duplicate resource binding name"))
		}
		seen[rb.Name] = true
	}

	return errs
}
