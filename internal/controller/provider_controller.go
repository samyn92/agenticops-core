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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
)

// ProviderReconciler reconciles a Provider object.
type ProviderReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=providers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=providers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=providers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Provider
	provider := &agentsv1alpha1.Provider{}
	if err := r.Get(ctx, req.NamespacedName, provider); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion: block while Agents still reference this Provider.
	if !provider.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, provider)
	}

	// Ensure the deletion-protection finalizer is present.
	if controllerutil.AddFinalizer(provider, agentsv1alpha1.ProviderFinalizer) {
		if err := r.Update(ctx, provider); err != nil {
			return ctrl.Result{}, err
		}
		// Re-fetch happens on the next reconcile triggered by the update.
		return ctrl.Result{}, nil
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(provider.DeepCopy())

	log.Info("Reconciling Provider", "name", provider.Name, "type", provider.Spec.Type)

	// 1. Validate config consistency
	configValid := r.validateConfig(provider)

	// 2. Check Secret existence
	secretReady := r.validateSecret(ctx, provider)

	// 3. Count bound agents
	boundAgents, err := r.countBoundAgents(ctx, provider)
	if err != nil {
		log.Error(err, "failed to count bound agents")
		// Non-fatal: don't fail the provider for this
	}
	provider.Status.BoundAgents = boundAgents

	// 4. Set overall phase
	if configValid && secretReady {
		provider.Status.Phase = agentsv1alpha1.ProviderPhaseReady
		provider.Status.Message = fmt.Sprintf("Provider %q (%s) is ready", provider.Name, provider.Spec.Type)
	} else {
		provider.Status.Phase = agentsv1alpha1.ProviderPhaseFailed
		if !secretReady {
			provider.Status.Message = "Secret not ready"
		} else {
			provider.Status.Message = "Config validation failed"
		}
	}

	log.Info("Provider reconciled", "phase", provider.Status.Phase, "boundAgents", boundAgents)

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, provider, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDelete blocks Provider deletion while Agents still reference it.
// The finalizer is only removed once boundAgents reaches zero, preventing
// silent breakage of running agents whose provider disappears.
func (r *ProviderReconciler) reconcileDelete(ctx context.Context, provider *agentsv1alpha1.Provider) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(provider, agentsv1alpha1.ProviderFinalizer) {
		return ctrl.Result{}, nil
	}

	boundAgents, err := r.countBoundAgents(ctx, provider)
	if err != nil {
		return ctrl.Result{}, err
	}

	if boundAgents > 0 {
		log.Info("Provider deletion blocked: still referenced by agents",
			"name", provider.Name, "boundAgents", boundAgents)
		statusPatch := client.MergeFrom(provider.DeepCopy())
		provider.Status.BoundAgents = boundAgents
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.ProviderConditionInUse,
			Status:  metav1.ConditionTrue,
			Reason:  "ReferencedByAgents",
			Message: fmt.Sprintf("deletion blocked: %d agent(s) still reference this provider", boundAgents),
		})
		if err := patchStatus(ctx, r.Client, provider, statusPatch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	log.Info("Provider deletion allowed: no bound agents, removing finalizer", "name", provider.Name)
	controllerutil.RemoveFinalizer(provider, agentsv1alpha1.ProviderFinalizer)
	if err := r.Update(ctx, provider); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// validateConfig validates that the config fields are consistent with spec.type.
func (r *ProviderReconciler) validateConfig(provider *agentsv1alpha1.Provider) bool {
	var issues []string

	cfg := provider.Spec.Config

	// Vertex is only valid for anthropic and google
	if cfg != nil && cfg.Vertex != nil {
		if provider.Spec.Type != agentsv1alpha1.ProviderTypeAnthropic && provider.Spec.Type != agentsv1alpha1.ProviderTypeGoogle {
			issues = append(issues, "vertex config is only valid for anthropic and google providers")
		}
	}

	// Bedrock is only valid for anthropic and bedrock
	if cfg != nil && cfg.Bedrock {
		if provider.Spec.Type != agentsv1alpha1.ProviderTypeAnthropic && provider.Spec.Type != agentsv1alpha1.ProviderTypeBedrock {
			issues = append(issues, "bedrock config is only valid for anthropic and bedrock providers")
		}
	}

	// Azure API version is only valid for azure
	if cfg != nil && cfg.AzureAPIVersion != "" {
		if provider.Spec.Type != agentsv1alpha1.ProviderTypeAzure {
			issues = append(issues, "azureAPIVersion is only valid for azure providers")
		}
	}

	// Organization/Project is only valid for openai
	if cfg != nil && (cfg.Organization != "" || cfg.Project != "") {
		if provider.Spec.Type != agentsv1alpha1.ProviderTypeOpenAI {
			issues = append(issues, "organization/project config is only valid for openai providers")
		}
	}

	// UseResponsesAPI is only valid for openai, azure, openaicompat
	if cfg != nil && cfg.UseResponsesAPI {
		if provider.Spec.Type != agentsv1alpha1.ProviderTypeOpenAI &&
			provider.Spec.Type != agentsv1alpha1.ProviderTypeAzure &&
			provider.Spec.Type != agentsv1alpha1.ProviderTypeOpenAICompat {
			issues = append(issues, "useResponsesAPI is only valid for openai, azure, and openaicompat providers")
		}
	}

	// openaicompat requires baseURL
	if provider.Spec.Type == agentsv1alpha1.ProviderTypeOpenAICompat {
		if provider.Spec.Endpoint == nil || provider.Spec.Endpoint.BaseURL == "" {
			issues = append(issues, "openaicompat providers require endpoint.baseURL")
		}
	}

	// Authentication: must use exactly one of apiKeySecret or
	// endpoint.oauth2ClientCredentials.
	hasAPIKey := provider.Spec.ApiKeySecret != nil
	hasOAuth := provider.Spec.Endpoint != nil && provider.Spec.Endpoint.OAuth2ClientCredentials != nil
	switch {
	case !hasAPIKey && !hasOAuth:
		issues = append(issues, "provider must set either spec.apiKeySecret or spec.endpoint.oauth2ClientCredentials")
	case hasAPIKey && hasOAuth:
		issues = append(issues, "provider must set only one of spec.apiKeySecret or spec.endpoint.oauth2ClientCredentials, not both")
	}

	if len(issues) > 0 {
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.ProviderConditionConfigValid,
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidConfig",
			Message: issues[0], // Report first issue
		})
		return false
	}

	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.ProviderConditionConfigValid,
		Status:  metav1.ConditionTrue,
		Reason:  "Valid",
		Message: fmt.Sprintf("Config is valid for type %s", provider.Spec.Type),
	})
	return true
}

// validateSecret checks that the referenced Secret exists and contains the expected key.
// When apiKeySecret is unset (oauth2 auth), it is a no-op that marks the
// SecretReady condition true with reason NotApplicable.
func (r *ProviderReconciler) validateSecret(ctx context.Context, provider *agentsv1alpha1.Provider) bool {
	if provider.Spec.ApiKeySecret == nil {
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.ProviderConditionSecretReady,
			Status:  metav1.ConditionTrue,
			Reason:  "NotApplicable",
			Message: "No apiKeySecret configured; authentication handled via OAuth2 client credentials",
		})
		return true
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      provider.Spec.ApiKeySecret.Name,
		Namespace: provider.Namespace,
	}, secret)

	if err != nil {
		reason := "SecretNotFound"
		message := fmt.Sprintf("Secret %q not found in namespace %q", provider.Spec.ApiKeySecret.Name, provider.Namespace)
		if !apierrors.IsNotFound(err) {
			reason = "SecretError"
			message = fmt.Sprintf("Failed to get secret %q: %v", provider.Spec.ApiKeySecret.Name, err)
		}
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.ProviderConditionSecretReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
		return false
	}

	// Check that the expected key exists
	if _, ok := secret.Data[provider.Spec.ApiKeySecret.Key]; !ok {
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.ProviderConditionSecretReady,
			Status:  metav1.ConditionFalse,
			Reason:  "KeyNotFound",
			Message: fmt.Sprintf("Secret %q does not contain key %q", provider.Spec.ApiKeySecret.Name, provider.Spec.ApiKeySecret.Key),
		})
		return false
	}

	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.ProviderConditionSecretReady,
		Status:  metav1.ConditionTrue,
		Reason:  "SecretExists",
		Message: fmt.Sprintf("Secret %q contains key %q", provider.Spec.ApiKeySecret.Name, provider.Spec.ApiKeySecret.Key),
	})
	return true
}

// countBoundAgents counts how many Agent CRs reference this provider via providerRefs.
func (r *ProviderReconciler) countBoundAgents(ctx context.Context, provider *agentsv1alpha1.Provider) (int, error) {
	agents := &agentsv1alpha1.AgentList{}
	if err := r.List(ctx, agents, client.InNamespace(provider.Namespace)); err != nil {
		return 0, err
	}

	count := 0
	for _, agent := range agents.Items {
		for _, ref := range agent.Spec.ProviderRefs {
			if ref.Name == provider.Name {
				count++
				break
			}
		}
	}
	return count, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.Provider{}).
		// Watch Secrets and enqueue the Provider that references them
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.secretToProviderRequests)).
		// Watch Agents to update boundAgents count
		Watches(&agentsv1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.agentToProviderRequests)).
		Named("provider").
		Complete(r)
}

// secretToProviderRequests maps a Secret change to the Provider(s) that reference it.
func (r *ProviderReconciler) secretToProviderRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	secret := obj.(*corev1.Secret)

	providers := &agentsv1alpha1.ProviderList{}
	if err := r.List(ctx, providers, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, prov := range providers.Items {
		if prov.Spec.ApiKeySecret == nil {
			continue
		}
		if prov.Spec.ApiKeySecret.Name == secret.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      prov.Name,
					Namespace: prov.Namespace,
				},
			})
		}
	}
	return requests
}

// agentToProviderRequests maps an Agent change to the Provider(s) it references.
func (r *ProviderReconciler) agentToProviderRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	agent := obj.(*agentsv1alpha1.Agent)

	seen := make(map[string]bool)
	var requests []reconcile.Request

	for _, ref := range agent.Spec.ProviderRefs {
		if !seen[ref.Name] {
			seen[ref.Name] = true
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      ref.Name,
					Namespace: agent.Namespace,
				},
			})
		}
	}

	return requests
}
