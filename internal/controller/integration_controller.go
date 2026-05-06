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

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
)

// IntegrationReconciler reconciles an Integration object.
type IntegrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=integrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=integrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=integrations/finalizers,verbs=update

func (r *IntegrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Integration
	res := &agentsv1alpha1.Integration{}
	if err := r.Get(ctx, req.NamespacedName, res); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(res.DeepCopy())

	log.Info("Reconciling Integration", "name", res.Name, "kind", res.Spec.Kind)

	// Validate the resource configuration
	if err := r.validateResource(res); err != nil {
		r.setFailedStatus(res, err.Error())
		if patchErr := patchStatus(ctx, r.Client, res, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Resource is declarative metadata — mark as Ready
	res.Status.Phase = agentsv1alpha1.IntegrationPhaseReady
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.IntegrationConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "Valid",
		Message: fmt.Sprintf("Integration %q (%s) is ready", res.Spec.DisplayName, res.Spec.Kind),
	})

	log.Info("Integration reconciled", "phase", res.Status.Phase, "kind", res.Spec.Kind)

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, res, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// validateResource performs basic validation that the kind-specific config is present.
// This is a safety net — most validation happens in the webhook.
func (r *IntegrationReconciler) validateResource(res *agentsv1alpha1.Integration) error {
	switch res.Spec.Kind {
	case agentsv1alpha1.IntegrationKindGitHubRepo:
		if res.Spec.GitHub == nil {
			return fmt.Errorf("github config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindGitHubOrg:
		if res.Spec.GitHubOrg == nil {
			return fmt.Errorf("githubOrg config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindGitLabProject:
		if res.Spec.GitLab == nil {
			return fmt.Errorf("gitlab config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindGitLabGroup:
		if res.Spec.GitLabGroup == nil {
			return fmt.Errorf("gitlabGroup config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindGitRepo:
		if res.Spec.Git == nil {
			return fmt.Errorf("git config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindS3Bucket:
		if res.Spec.S3 == nil {
			return fmt.Errorf("s3 config is required for kind=%s", res.Spec.Kind)
		}
	case agentsv1alpha1.IntegrationKindDocumentation:
		if res.Spec.Documentation == nil {
			return fmt.Errorf("documentation config is required for kind=%s", res.Spec.Kind)
		}
	default:
		return fmt.Errorf("unknown resource kind: %s", res.Spec.Kind)
	}
	return nil
}

// setFailedStatus sets the Integration status to Failed. Caller must patch status.
func (r *IntegrationReconciler) setFailedStatus(res *agentsv1alpha1.Integration, message string) {
	res.Status.Phase = agentsv1alpha1.IntegrationPhaseFailed
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.IntegrationConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.Integration{}).
		Named("integration").
		Complete(r)
}
