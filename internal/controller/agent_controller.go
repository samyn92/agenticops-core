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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	"github.com/samyn92/agentops-core/internal/resources"
)

// AgentReconciler reconciles an Agent object.
type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=agents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agenttools,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agentresources,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=providers,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Agent
	agent := &agentsv1alpha1.Agent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(agent.DeepCopy())

	log.Info("Reconciling Agent", "name", agent.Name, "mode", agent.Spec.Mode)

	// Resolve referenced AgentTools
	agentTools, err := r.resolveAgentTools(ctx, agent)
	if err != nil {
		r.setAgentFailedStatus(agent, agentsv1alpha1.AgentPhaseFailed, err.Error())
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Validate AgentTools are ready
	if err := r.validateAgentToolsReady(agentTools); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionToolsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "AgentToolNotReady",
			Message: err.Error(),
		})
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Set ToolsReady condition
	if len(agent.Spec.Tools) > 0 {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionToolsReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllReady",
			Message: fmt.Sprintf("%d tools bound", len(agentTools)),
		})
	}

	// Resolve referenced AgentResources
	agentResources, err := r.resolveAgentResources(ctx, agent)
	if err != nil {
		r.setAgentFailedStatus(agent, agentsv1alpha1.AgentPhaseFailed, err.Error())
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Validate AgentResources are ready
	if err := r.validateAgentResourcesReady(agentResources); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionResourcesReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ResourceNotReady",
			Message: err.Error(),
		})
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Set ResourcesReady condition
	if len(agent.Spec.ResourceBindings) > 0 {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionResourcesReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllReady",
			Message: fmt.Sprintf("%d resources bound", len(agentResources)),
		})
	}

	// Set ProvidersReady condition
	// Resolve Provider CRs from providerRefs
	resolvedProviders, err := r.resolveProviders(ctx, agent)
	if err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionProvidersReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ProviderNotFound",
			Message: err.Error(),
		})
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Validate Provider CRs are ready
	if err := r.validateProvidersReady(resolvedProviders); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionProvidersReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ProviderNotReady",
			Message: err.Error(),
		})
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	totalProviders := len(resolvedProviders)
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionProvidersReady,
		Status:  metav1.ConditionTrue,
		Reason:  "Configured",
		Message: fmt.Sprintf("%d providers registered", totalProviders),
	})

	// Branch by mode
	var result ctrl.Result
	var reconcileErr error

	switch agent.Spec.Mode {
	case agentsv1alpha1.AgentModeDaemon:
		result, reconcileErr = r.reconcileDaemon(ctx, agent, agentTools, agentResources, resolvedProviders)
	case agentsv1alpha1.AgentModeTask:
		result, reconcileErr = r.reconcileTask(ctx, agent, agentTools, agentResources, resolvedProviders)
	default:
		return ctrl.Result{}, fmt.Errorf("unknown agent mode: %s", agent.Spec.Mode)
	}

	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, agent, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

// hasMCPSourceTools returns true if any of the resolved AgentTools have an MCP source.
func hasMCPSourceTools(agentTools []agentsv1alpha1.AgentTool) bool {
	for i := range agentTools {
		if agentTools[i].IsMCPSource() {
			return true
		}
	}
	return false
}

// reconcileDaemon handles daemon mode: RBAC -> PVC -> ConfigMaps -> Deployment -> Service -> NetworkPolicy -> status.
//
//nolint:unparam // Result is always nil for now but will be used for requeue logic.
func (r *AgentReconciler) reconcileDaemon(ctx context.Context, agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, agentResources []agentsv1alpha1.AgentResource, providers []agentsv1alpha1.Provider) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 0. RBAC (ServiceAccount + Role + RoleBinding) — only if the user
	//    did not bring their own ServiceAccount.
	if agent.Spec.ServiceAccountName == "" {
		if err := r.reconcileAgentRBAC(ctx, agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 1. PVC
	if agent.Spec.Storage != nil {
		pvc := resources.BuildAgentPVC(agent)
		if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, pvc); err != nil {
			return ctrl.Result{}, err
		}
		agent.Status.StoragePVC = pvc.Name
	}

	// 2. Operator extension ConfigMap
	configMap, err := resources.BuildAgentConfigMap(agent, agentResources, agentTools, providers)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, configMap); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Gateway ConfigMap (if MCP-source tools)
	if hasMCPSourceTools(agentTools) {
		gwCM, err := resources.BuildGatewayConfigMap(agent, agentTools)
		if err != nil {
			return ctrl.Result{}, err
		}
		if gwCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, gwCM); err != nil {
				return ctrl.Result{}, err
			}
		}

		// 4. MCP ConfigMap (mcp.json for runtime MCP adapter)
		mcpCM, err := resources.BuildMCPConfigMap(agent, agentTools)
		if err != nil {
			return ctrl.Result{}, err
		}
		if mcpCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, mcpCM); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 5. Deployment
	deployment := resources.BuildAgentDeployment(agent, agentTools, providers)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, deployment); err != nil {
		return ctrl.Result{}, err
	}

	// 6. Service
	service := resources.BuildAgentService(agent)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, service); err != nil {
		return ctrl.Result{}, err
	}

	// 7. NetworkPolicy (if enabled)
	if agent.Spec.NetworkPolicy != nil && agent.Spec.NetworkPolicy.Enabled {
		netpol := resources.BuildAgentNetworkPolicy(agent)
		if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, netpol); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 8. Update status
	// Read the actual deployment to get ready replicas
	actualDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, actualDeploy); err == nil {
		agent.Status.ReadyReplicas = actualDeploy.Status.ReadyReplicas
	}

	agent.Status.ServiceURL = resources.AgentServiceURL(agent)
	agent.Status.ActiveModel = agent.Spec.Model

	// Tools ready condition
	toolCount := agent.BuiltinToolCount() + len(agent.Spec.Tools)
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionToolsReady,
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: fmt.Sprintf("%d tool packages configured", toolCount),
	})

	if agent.Status.ReadyReplicas > 0 {
		agent.Status.Phase = agentsv1alpha1.AgentPhaseRunning
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:   agentsv1alpha1.AgentConditionReady,
			Status: metav1.ConditionTrue,
			Reason: "Running",
		})
	} else {
		agent.Status.Phase = agentsv1alpha1.AgentPhasePending
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "Pending",
			Message: "Waiting for deployment to be ready",
		})
	}

	log.Info("Daemon agent reconciled", "phase", agent.Status.Phase)

	// Requeue to poll for readiness since we filter Deployment status-only updates
	// via GenerationChangedPredicate.
	if agent.Status.Phase != agentsv1alpha1.AgentPhaseRunning {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// reconcileTask handles task mode: RBAC -> ConfigMaps -> status (Ready).
//
//nolint:unparam // Result is always nil for now but will be used for requeue logic.
func (r *AgentReconciler) reconcileTask(ctx context.Context, agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, agentResources []agentsv1alpha1.AgentResource, providers []agentsv1alpha1.Provider) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 0. RBAC (ServiceAccount + Role + RoleBinding) — only if the user
	//    did not bring their own ServiceAccount.
	if agent.Spec.ServiceAccountName == "" {
		if err := r.reconcileAgentRBAC(ctx, agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 1. Operator extension ConfigMap
	configMap, err := resources.BuildAgentConfigMap(agent, agentResources, agentTools, providers)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, configMap); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Gateway ConfigMap (if MCP-source tools)
	if hasMCPSourceTools(agentTools) {
		gwCM, err := resources.BuildGatewayConfigMap(agent, agentTools)
		if err != nil {
			return ctrl.Result{}, err
		}
		if gwCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, gwCM); err != nil {
				return ctrl.Result{}, err
			}
		}

		mcpCM, err := resources.BuildMCPConfigMap(agent, agentTools)
		if err != nil {
			return ctrl.Result{}, err
		}
		if mcpCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, mcpCM); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 3. Update status
	agent.Status.Phase = agentsv1alpha1.AgentPhaseReady
	agent.Status.ActiveModel = agent.Spec.Model

	toolCount := agent.BuiltinToolCount() + len(agent.Spec.Tools)
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionToolsReady,
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: fmt.Sprintf("%d tool packages configured", toolCount),
	})

	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentConditionReady,
		Status: metav1.ConditionTrue,
		Reason: "Ready",
	})

	log.Info("Task agent reconciled", "phase", agent.Status.Phase)
	return ctrl.Result{}, nil
}

// reconcileAgentRBAC creates/updates the ServiceAccount, Role, and RoleBinding
// for an agent so its pods can create AgentRun CRs and read Agent CRs.
func (r *AgentReconciler) reconcileAgentRBAC(ctx context.Context, agent *agentsv1alpha1.Agent) error {
	sa := resources.BuildAgentServiceAccount(agent)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, sa); err != nil {
		return err
	}

	role := resources.BuildAgentRole(agent)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, role); err != nil {
		return err
	}

	rb := resources.BuildAgentRoleBinding(agent)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, rb); err != nil {
		return err
	}

	return nil
}

// resolveAgentTools fetches all AgentTool CRs referenced by the agent.
func (r *AgentReconciler) resolveAgentTools(ctx context.Context, agent *agentsv1alpha1.Agent) ([]agentsv1alpha1.AgentTool, error) {
	tools := make([]agentsv1alpha1.AgentTool, 0, len(agent.Spec.Tools))
	for _, binding := range agent.Spec.Tools {
		tool := &agentsv1alpha1.AgentTool{}
		if err := r.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: agent.Namespace}, tool); err != nil {
			return nil, fmt.Errorf("AgentTool %q not found: %w", binding.Name, err)
		}
		tools = append(tools, *tool)
	}
	return tools, nil
}

// resolveAgentResources fetches all AgentResource CRs referenced by the agent.
func (r *AgentReconciler) resolveAgentResources(ctx context.Context, agent *agentsv1alpha1.Agent) ([]agentsv1alpha1.AgentResource, error) {
	agentRes := make([]agentsv1alpha1.AgentResource, 0, len(agent.Spec.ResourceBindings))
	for _, binding := range agent.Spec.ResourceBindings {
		res := &agentsv1alpha1.AgentResource{}
		if err := r.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: agent.Namespace}, res); err != nil {
			return nil, fmt.Errorf("AgentResource %q not found: %w", binding.Name, err)
		}
		agentRes = append(agentRes, *res)
	}
	return agentRes, nil
}

// validateAgentToolsReady checks all referenced AgentTools are in Ready phase.
func (r *AgentReconciler) validateAgentToolsReady(tools []agentsv1alpha1.AgentTool) error {
	for _, tool := range tools {
		if tool.Status.Phase != agentsv1alpha1.AgentToolPhaseReady {
			return fmt.Errorf("AgentTool %q is not Ready (phase: %s)", tool.Name, tool.Status.Phase)
		}
	}
	return nil
}

// validateAgentResourcesReady checks all referenced AgentResources are in Ready phase.
func (r *AgentReconciler) validateAgentResourcesReady(agentRes []agentsv1alpha1.AgentResource) error {
	for _, res := range agentRes {
		if res.Status.Phase != agentsv1alpha1.AgentResourcePhaseReady {
			return fmt.Errorf("AgentResource %q is not Ready (phase: %s)", res.Name, res.Status.Phase)
		}
	}
	return nil
}

// resolveProviders fetches all Provider CRs referenced by the agent via providerRefs.
func (r *AgentReconciler) resolveProviders(ctx context.Context, agent *agentsv1alpha1.Agent) ([]agentsv1alpha1.Provider, error) {
	providers := make([]agentsv1alpha1.Provider, 0, len(agent.Spec.ProviderRefs))
	for _, ref := range agent.Spec.ProviderRefs {
		prov := &agentsv1alpha1.Provider{}
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: agent.Namespace}, prov); err != nil {
			return nil, fmt.Errorf("provider %q not found: %w", ref.Name, err)
		}
		providers = append(providers, *prov)
	}
	return providers, nil
}

// validateProvidersReady checks all referenced Provider CRs are in Ready phase.
func (r *AgentReconciler) validateProvidersReady(providers []agentsv1alpha1.Provider) error {
	for _, prov := range providers {
		if prov.Status.Phase != agentsv1alpha1.ProviderPhaseReady {
			return fmt.Errorf("provider %q is not Ready (phase: %s)", prov.Name, prov.Status.Phase)
		}
	}
	return nil
}

// setAgentFailedStatus sets the Agent status to a failed phase. Caller must patch status.
func (r *AgentReconciler) setAgentFailedStatus(agent *agentsv1alpha1.Agent, phase agentsv1alpha1.AgentPhase, message string) {
	agent.Status.Phase = phase
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  string(phase),
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.Agent{}).
		Owns(&appsv1.Deployment{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Named("agent").
		Complete(r)
}
