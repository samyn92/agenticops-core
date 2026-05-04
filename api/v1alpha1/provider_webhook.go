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

var providerlog = logf.Log.WithName("provider-webhook")

// SetupProviderWebhookWithManager registers the Provider validating webhook.
func (r *Provider) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-provider,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=providers,verbs=create;update,versions=v1alpha1,name=vprovider.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*Provider] = &Provider{}

// ValidateCreate implements admission.Validator.
func (r *Provider) ValidateCreate(_ context.Context, obj *Provider) (admission.Warnings, error) {
	providerlog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *Provider) ValidateUpdate(_ context.Context, _ *Provider, newObj *Provider) (admission.Warnings, error) {
	providerlog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *Provider) ValidateDelete(_ context.Context, _ *Provider) (admission.Warnings, error) {
	// TODO: prevent deletion if agents still reference this provider
	return nil, nil
}

func (r *Provider) validate() (admission.Warnings, error) {
	allErrs := make(field.ErrorList, 0, 8)
	specPath := field.NewPath("spec")

	allErrs = append(allErrs, r.validateType(specPath)...)
	allErrs = append(allErrs, r.validateEndpoint(specPath)...)
	allErrs = append(allErrs, r.validateConfig(specPath)...)
	allErrs = append(allErrs, r.validateDefaults(specPath)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Provider"},
			r.Name, allErrs)
	}

	return nil, nil
}

// validateType checks the provider type is valid.
func (r *Provider) validateType(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	validTypes := map[ProviderType]bool{
		ProviderTypeAnthropic:    true,
		ProviderTypeOpenAI:       true,
		ProviderTypeGoogle:       true,
		ProviderTypeAzure:        true,
		ProviderTypeBedrock:      true,
		ProviderTypeOpenRouter:   true,
		ProviderTypeOpenAICompat: true,
	}

	if !validTypes[r.Spec.Type] {
		errs = append(errs, field.Invalid(specPath.Child("type"), r.Spec.Type,
			"must be one of: anthropic, openai, google, azure, bedrock, openrouter, openaicompat"))
	}

	return errs
}

// validateEndpoint validates endpoint configuration.
func (r *Provider) validateEndpoint(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	// openaicompat requires baseURL
	if r.Spec.Type == ProviderTypeOpenAICompat {
		if r.Spec.Endpoint == nil || r.Spec.Endpoint.BaseURL == "" {
			errs = append(errs, field.Required(specPath.Child("endpoint", "baseURL"),
				"openaicompat providers require a base URL"))
		}
	}

	return errs
}

// validateConfig validates type-specific config consistency.
func (r *Provider) validateConfig(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	cfg := r.Spec.Config
	if cfg == nil {
		return errs
	}

	configPath := specPath.Child("config")

	// Vertex is only valid for anthropic and google
	if cfg.Vertex != nil {
		if r.Spec.Type != ProviderTypeAnthropic && r.Spec.Type != ProviderTypeGoogle {
			errs = append(errs, field.Forbidden(configPath.Child("vertex"),
				"vertex config is only valid for anthropic and google providers"))
		}
	}

	// Bedrock is only valid for anthropic and bedrock
	if cfg.Bedrock {
		if r.Spec.Type != ProviderTypeAnthropic && r.Spec.Type != ProviderTypeBedrock {
			errs = append(errs, field.Forbidden(configPath.Child("bedrock"),
				"bedrock config is only valid for anthropic and bedrock providers"))
		}
	}

	// Vertex and Bedrock are mutually exclusive (both route anthropic differently)
	if cfg.Vertex != nil && cfg.Bedrock {
		errs = append(errs, field.Forbidden(configPath.Child("bedrock"),
			"vertex and bedrock are mutually exclusive"))
	}

	// Azure API version is only valid for azure
	if cfg.AzureAPIVersion != "" {
		if r.Spec.Type != ProviderTypeAzure {
			errs = append(errs, field.Forbidden(configPath.Child("azureAPIVersion"),
				"azureAPIVersion is only valid for azure providers"))
		}
	}

	// Organization/Project is only valid for openai
	if cfg.Organization != "" || cfg.Project != "" {
		if r.Spec.Type != ProviderTypeOpenAI {
			if cfg.Organization != "" {
				errs = append(errs, field.Forbidden(configPath.Child("organization"),
					"organization is only valid for openai providers"))
			}
			if cfg.Project != "" {
				errs = append(errs, field.Forbidden(configPath.Child("project"),
					"project is only valid for openai providers"))
			}
		}
	}

	// UseResponsesAPI is only valid for openai, azure, openaicompat
	if cfg.UseResponsesAPI {
		validFor := map[ProviderType]bool{
			ProviderTypeOpenAI:       true,
			ProviderTypeAzure:        true,
			ProviderTypeOpenAICompat: true,
		}
		if !validFor[r.Spec.Type] {
			errs = append(errs, field.Forbidden(configPath.Child("useResponsesAPI"),
				"useResponsesAPI is only valid for openai, azure, and openaicompat providers"))
		}
	}

	return errs
}

// validateDefaults validates per-call default options.
func (r *Provider) validateDefaults(specPath *field.Path) field.ErrorList {
	var errs field.ErrorList

	defs := r.Spec.Defaults
	if defs == nil {
		return errs
	}

	defaultsPath := specPath.Child("defaults")

	// Anthropic defaults should only be set for anthropic/bedrock types
	if defs.Anthropic != nil {
		if r.Spec.Type != ProviderTypeAnthropic && r.Spec.Type != ProviderTypeBedrock {
			errs = append(errs, field.Forbidden(defaultsPath.Child("anthropic"),
				fmt.Sprintf("anthropic call defaults are not valid for %s providers", r.Spec.Type)))
		}
	}

	// OpenAI defaults should only be set for openai/azure/openaicompat types
	if defs.OpenAI != nil {
		validFor := map[ProviderType]bool{
			ProviderTypeOpenAI:       true,
			ProviderTypeAzure:        true,
			ProviderTypeOpenAICompat: true,
			ProviderTypeOpenRouter:   true,
		}
		if !validFor[r.Spec.Type] {
			errs = append(errs, field.Forbidden(defaultsPath.Child("openai"),
				fmt.Sprintf("openai call defaults are not valid for %s providers", r.Spec.Type)))
		}
	}

	// Google defaults should only be set for google types
	if defs.Google != nil {
		if r.Spec.Type != ProviderTypeGoogle {
			errs = append(errs, field.Forbidden(defaultsPath.Child("google"),
				fmt.Sprintf("google call defaults are not valid for %s providers", r.Spec.Type)))
		}
		// ThinkingLevel and ThinkingBudgetTokens are mutually exclusive
		if defs.Google.ThinkingLevel != "" && defs.Google.ThinkingBudgetTokens != nil {
			errs = append(errs, field.Forbidden(defaultsPath.Child("google", "thinkingBudgetTokens"),
				"thinkingLevel and thinkingBudgetTokens are mutually exclusive"))
		}
	}

	return errs
}
