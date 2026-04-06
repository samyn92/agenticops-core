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

// validBuiltinTools is the set of valid Pi built-in tool names.
var validBuiltinTools = map[string]bool{
	"read": true, "bash": true, "edit": true, "write": true,
	"grep": true, "find": true, "ls": true,
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
	var allErrs field.ErrorList
	var warnings admission.Warnings
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, r.validateMode(specPath)...)
	errs, warns := r.validateProviderAndTools(specPath)
	allErrs = append(allErrs, errs...)
	warnings = append(warnings, warns...)
	errs, warns = r.validateToolHooks(specPath)
	allErrs = append(allErrs, errs...)
	warnings = append(warnings, warns...)
	allErrs = append(allErrs, r.validateResourceRefs(specPath)...)

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
		if r.Spec.Compaction != nil {
			errs = append(errs, field.Forbidden(specPath.Child("compaction"),
				"compaction is daemon-only"))
		}
	}

	return errs
}

func (r *Agent) validateProviderAndTools(specPath *field.Path) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList

	// Providers required
	if len(r.Spec.Providers) == 0 {
		errs = append(errs, field.Required(specPath.Child("providers"),
			"all agents need at least one LLM provider"))
	}

	// At least one tool source
	if len(r.Spec.ToolRefs) == 0 && len(r.Spec.BuiltinTools) == 0 {
		errs = append(errs, field.Required(specPath.Child("toolRefs"),
			"agents need at least one tool (builtinTools or toolRefs)"))
	}

	// Validate builtinTools names
	for i, tool := range r.Spec.BuiltinTools {
		if !validBuiltinTools[tool] {
			errs = append(errs, field.Invalid(
				specPath.Child("builtinTools").Index(i), tool,
				fmt.Sprintf("valid names are: read, bash, edit, write, grep, find, ls")))
		}
	}

	// Schedule requires schedulePrompt
	if r.Spec.Schedule != "" && r.Spec.SchedulePrompt == "" {
		errs = append(errs, field.Required(specPath.Child("schedulePrompt"),
			"schedulePrompt is required when schedule is set"))
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

	// Compaction strategy validation
	if r.Spec.Compaction != nil {
		validStrategies := map[string]bool{"auto": true, "manual": true, "off": true, "": true}
		if !validStrategies[r.Spec.Compaction.Strategy] {
			errs = append(errs, field.Invalid(
				specPath.Child("compaction").Child("strategy"), r.Spec.Compaction.Strategy,
				"must be auto, manual, or off"))
		}
	}

	return errs, nil
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

	return errs
}
