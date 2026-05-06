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

var integrationlog = logf.Log.WithName("integration-webhook")

// SetupIntegrationWebhookWithManager registers the Integration validating webhook.
func (r *Integration) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-integration,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=integrations,verbs=create;update,versions=v1alpha1,name=vintegration.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*Integration] = &Integration{}

// ValidateCreate implements admission.Validator.
func (r *Integration) ValidateCreate(_ context.Context, obj *Integration) (admission.Warnings, error) {
	integrationlog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *Integration) ValidateUpdate(_ context.Context, _ *Integration, newObj *Integration) (admission.Warnings, error) {
	integrationlog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *Integration) ValidateDelete(_ context.Context, _ *Integration) (admission.Warnings, error) {
	return nil, nil
}

func (r *Integration) validate() (admission.Warnings, error) {
	allErrs := make(field.ErrorList, 0, 8)
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, r.validateKindConfig(specPath)...)
	allErrs = append(allErrs, r.validateTriggers(specPath)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Integration"},
			r.Name, allErrs)
	}

	return nil, nil
}

// validateKindConfig ensures the kind-specific config block matches the kind field.
func (r *Integration) validateKindConfig(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	configPresent := map[IntegrationKind]bool{
		IntegrationKindGitHubRepo:    r.Spec.GitHub != nil,
		IntegrationKindGitHubOrg:     r.Spec.GitHubOrg != nil,
		IntegrationKindGitLabProject: r.Spec.GitLab != nil,
		IntegrationKindGitLabGroup:   r.Spec.GitLabGroup != nil,
		IntegrationKindGitRepo:       r.Spec.Git != nil,
		IntegrationKindS3Bucket:      r.Spec.S3 != nil,
		IntegrationKindDocumentation: r.Spec.Documentation != nil,
	}

	kindToField := map[IntegrationKind]string{
		IntegrationKindGitHubRepo:    "github",
		IntegrationKindGitHubOrg:     "githubOrg",
		IntegrationKindGitLabProject: "gitlab",
		IntegrationKindGitLabGroup:   "gitlabGroup",
		IntegrationKindGitRepo:       "git",
		IntegrationKindS3Bucket:      "s3",
		IntegrationKindDocumentation: "documentation",
	}

	fieldName, ok := kindToField[r.Spec.Kind]
	if ok && !configPresent[r.Spec.Kind] {
		errs = append(errs, field.Required(specPath.Child(fieldName),
			fmt.Sprintf("%s config is required for kind=%s", fieldName, r.Spec.Kind)))
	}

	for kind, present := range configPresent {
		if present && kind != r.Spec.Kind {
			otherField := kindToField[kind]
			errs = append(errs, field.Forbidden(specPath.Child(otherField),
				fmt.Sprintf("%s config is not allowed for kind=%s", otherField, r.Spec.Kind)))
		}
	}

	return errs
}

// validateTriggers validates each trigger entry.
func (r *Integration) validateTriggers(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	for i, t := range r.Spec.Triggers {
		triggerPath := specPath.Child("triggers").Index(i)
		if t.On == "" {
			errs = append(errs, field.Required(triggerPath.Child("on"), "event type is required"))
		}
		if t.AgentRef == "" {
			errs = append(errs, field.Required(triggerPath.Child("agentRef"), "agent reference is required"))
		}
		if t.Prompt == "" {
			errs = append(errs, field.Required(triggerPath.Child("prompt"), "prompt template is required"))
		}
	}

	return errs
}
