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
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var agtoollog = logf.Log.WithName("agenttool-webhook")

// SetupWebhookWithManager registers the AgentTool validating webhook.
func (r *AgentTool) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-agenttool,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=agenttools,verbs=create;update,versions=v1alpha1,name=vagenttool.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*AgentTool] = &AgentTool{}

// ValidateCreate implements admission.Validator.
func (r *AgentTool) ValidateCreate(_ context.Context, obj *AgentTool) (admission.Warnings, error) {
	agtoollog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *AgentTool) ValidateUpdate(_ context.Context, _ *AgentTool, newObj *AgentTool) (admission.Warnings, error) {
	agtoollog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *AgentTool) ValidateDelete(_ context.Context, _ *AgentTool) (admission.Warnings, error) {
	return nil, nil
}

func (r *AgentTool) validate() (admission.Warnings, error) {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Count how many source blocks are set
	sourceCount := 0
	if r.Spec.OCI != nil {
		sourceCount++
	}
	if r.Spec.ConfigMap != nil {
		sourceCount++
	}
	if r.Spec.Inline != nil {
		sourceCount++
	}
	if r.Spec.MCPServer != nil {
		sourceCount++
	}
	if r.Spec.MCPEndpoint != nil {
		sourceCount++
	}
	if r.Spec.Skill != nil {
		sourceCount++
	}

	if sourceCount == 0 {
		allErrs = append(allErrs, field.Required(specPath,
			"exactly one source must be set: oci, configMap, inline, mcpServer, mcpEndpoint, or skill"))
	}
	if sourceCount > 1 {
		allErrs = append(allErrs, field.Invalid(specPath, sourceCount,
			"exactly one source must be set; found multiple"))
	}

	// Source-specific validation
	if r.Spec.OCI != nil {
		if r.Spec.OCI.Ref == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("oci", "ref"),
				"OCI reference is required"))
		} else if err := validateOCIRefFormat(r.Spec.OCI.Ref); err != nil {
			allErrs = append(allErrs, field.Invalid(specPath.Child("oci", "ref"),
				r.Spec.OCI.Ref, err.Error()))
		}
	}

	if r.Spec.ConfigMap != nil {
		if r.Spec.ConfigMap.Name == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("configMap", "name"),
				"ConfigMap name is required"))
		}
		if r.Spec.ConfigMap.Key == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("configMap", "key"),
				"ConfigMap key is required"))
		}
	}

	if r.Spec.Inline != nil {
		if r.Spec.Inline.Content == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("inline", "content"),
				"inline content is required"))
		}
		if len(r.Spec.Inline.Content) > 4096 {
			allErrs = append(allErrs, field.Invalid(specPath.Child("inline", "content"),
				len(r.Spec.Inline.Content),
				"inline content must be < 4KB"))
		}
	}

	if r.Spec.MCPServer != nil {
		if r.Spec.MCPServer.Image == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("mcpServer", "image"),
				"image is required for mcpServer source"))
		}
	}

	if r.Spec.MCPEndpoint != nil {
		if r.Spec.MCPEndpoint.URL == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("mcpEndpoint", "url"),
				"URL is required for mcpEndpoint source"))
		}
	}

	if r.Spec.Skill != nil {
		if r.Spec.Skill.Ref == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("skill", "ref"),
				"OCI reference is required for skill source"))
		} else if err := validateOCIRefFormat(r.Spec.Skill.Ref); err != nil {
			allErrs = append(allErrs, field.Invalid(specPath.Child("skill", "ref"),
				r.Spec.Skill.Ref, err.Error()))
		}
	}

	// Validate defaultPermissions mode/rules only make sense for MCP sources
	if r.Spec.DefaultPermissions != nil && r.Spec.DefaultPermissions.Mode != "" {
		if r.Spec.MCPServer == nil && r.Spec.MCPEndpoint == nil {
			allErrs = append(allErrs, field.Invalid(
				specPath.Child("defaultPermissions", "mode"),
				r.Spec.DefaultPermissions.Mode,
				"deny/allow mode is only valid for mcpServer or mcpEndpoint sources"))
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "AgentTool"},
			r.Name, allErrs)
	}

	return nil, nil
}

// ociRefPattern matches valid OCI image references.
// Allows: alphanumeric, dots, dashes, underscores, colons, slashes, @, +
// Rejects: shell metacharacters (;, |, &, $, `, (, ), etc.)
var ociRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-/:@+]+$`)

// validateOCIRefFormat checks that an OCI reference contains no shell metacharacters.
func validateOCIRefFormat(ref string) error {
	if !ociRefPattern.MatchString(ref) {
		return fmt.Errorf("contains invalid characters; only alphanumeric, '.', '-', '_', '/', ':', '@' are allowed")
	}
	return nil
}
