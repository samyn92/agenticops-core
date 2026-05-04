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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var agentresourcelog = logf.Log.WithName("agentresource-webhook")

// SetupAgentResourceWebhookWithManager registers the AgentResource validating webhook.
func (r *AgentResource) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-agentresource,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=agentresources,verbs=create;update,versions=v1alpha1,name=vagentresource.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*AgentResource] = &AgentResource{}

// ValidateCreate implements admission.Validator.
func (r *AgentResource) ValidateCreate(_ context.Context, obj *AgentResource) (admission.Warnings, error) {
	agentresourcelog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *AgentResource) ValidateUpdate(_ context.Context, _ *AgentResource, newObj *AgentResource) (admission.Warnings, error) {
	agentresourcelog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *AgentResource) ValidateDelete(_ context.Context, _ *AgentResource) (admission.Warnings, error) {
	return nil, nil
}

func (r *AgentResource) validate() (admission.Warnings, error) {
	allErrs := make(field.ErrorList, 0, 8)
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, r.validateKindConfig(specPath)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "AgentResource"},
			r.Name, allErrs)
	}

	return nil, nil
}

// validateKindConfig ensures the kind-specific config block matches the kind field.
func (r *AgentResource) validateKindConfig(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// Map of kind -> whether the corresponding config block is set (7 kinds)
	configPresent := map[AgentResourceKind]bool{
		AgentResourceKindGitHubRepo:    r.Spec.GitHub != nil,
		AgentResourceKindGitHubOrg:     r.Spec.GitHubOrg != nil,
		AgentResourceKindGitLabProject: r.Spec.GitLab != nil,
		AgentResourceKindGitLabGroup:   r.Spec.GitLabGroup != nil,
		AgentResourceKindGitRepo:       r.Spec.Git != nil,
		AgentResourceKindS3Bucket:      r.Spec.S3 != nil,
		AgentResourceKindDocumentation: r.Spec.Documentation != nil,
	}

	// Map kind to its config field name
	kindToField := map[AgentResourceKind]string{
		AgentResourceKindGitHubRepo:    "github",
		AgentResourceKindGitHubOrg:     "githubOrg",
		AgentResourceKindGitLabProject: "gitlab",
		AgentResourceKindGitLabGroup:   "gitlabGroup",
		AgentResourceKindGitRepo:       "git",
		AgentResourceKindS3Bucket:      "s3",
		AgentResourceKindDocumentation: "documentation",
	}

	// The config block matching the kind must be present
	fieldName, ok := kindToField[r.Spec.Kind]
	if ok && !configPresent[r.Spec.Kind] {
		errs = append(errs, field.Required(specPath.Child(fieldName),
			fmt.Sprintf("%s config is required for kind=%s", fieldName, r.Spec.Kind)))
	}

	// No other config blocks should be set
	for kind, present := range configPresent {
		if present && kind != r.Spec.Kind {
			otherField := kindToField[kind]
			errs = append(errs, field.Forbidden(specPath.Child(otherField),
				fmt.Sprintf("%s config is not allowed for kind=%s", otherField, r.Spec.Kind)))
		}
	}

	return errs
}
