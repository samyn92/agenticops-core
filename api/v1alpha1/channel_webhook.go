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

var channellog = logf.Log.WithName("channel-webhook")

// SetupChannelWebhookWithManager registers the Channel validating webhook.
func (r *Channel) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-agents-agentops-io-v1alpha1-channel,mutating=false,failurePolicy=fail,sideEffects=None,groups=agents.agentops.io,resources=channels,verbs=create;update,versions=v1alpha1,name=vchannel.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*Channel] = &Channel{}

// ValidateCreate implements admission.Validator.
func (r *Channel) ValidateCreate(_ context.Context, obj *Channel) (admission.Warnings, error) {
	channellog.Info("validate create", "name", r.Name)
	return obj.validate()
}

// ValidateUpdate implements admission.Validator.
func (r *Channel) ValidateUpdate(_ context.Context, _ *Channel, newObj *Channel) (admission.Warnings, error) {
	channellog.Info("validate update", "name", r.Name)
	return newObj.validate()
}

// ValidateDelete implements admission.Validator.
func (r *Channel) ValidateDelete(_ context.Context, _ *Channel) (admission.Warnings, error) {
	return nil, nil
}

func (r *Channel) validate() (admission.Warnings, error) {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Platform config must match type
	switch r.Spec.Type {
	case ChannelTypeTelegram:
		if r.Spec.Telegram == nil {
			allErrs = append(allErrs, field.Required(specPath.Child("telegram"),
				"telegram config is required for type=telegram"))
		}
	case ChannelTypeSlack:
		if r.Spec.Slack == nil {
			allErrs = append(allErrs, field.Required(specPath.Child("slack"),
				"slack config is required for type=slack"))
		}
	case ChannelTypeDiscord:
		if r.Spec.Discord == nil {
			allErrs = append(allErrs, field.Required(specPath.Child("discord"),
				"discord config is required for type=discord"))
		}
	case ChannelTypeGitLab:
		if r.Spec.GitLab == nil {
			allErrs = append(allErrs, field.Required(specPath.Child("gitlab"),
				"gitlab config is required for type=gitlab"))
		}
	case ChannelTypeGitHub:
		if r.Spec.GitHub == nil {
			allErrs = append(allErrs, field.Required(specPath.Child("github"),
				"github config is required for type=github"))
		}
	}

	// Event types require prompt template
	if r.Spec.Type.IsEventType() && r.Spec.Prompt == "" {
		allErrs = append(allErrs, field.Required(specPath.Child("prompt"),
			fmt.Sprintf("prompt template is required for event-type channel (%s)", r.Spec.Type)))
	}

	// Note: chat type + task agent validation requires cross-resource lookup
	// which is done in the controller, not here. The webhook can only validate
	// the Channel spec itself. However, we add a note in the error if someone
	// uses a chat type without the fields that suggest a daemon target.

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Channel"},
			r.Name, allErrs)
	}

	return nil, nil
}
