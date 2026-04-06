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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	"github.com/samyn92/agenticops-core/internal/resources"
)

// AgentReconciler reconciles an Agent object.
type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=mcpservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
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

	// Resolve referenced MCPServers
	mcpServers, err := r.resolveMCPServers(ctx, agent)
	if err != nil {
		r.setAgentFailedStatus(agent, agentsv1alpha1.AgentPhaseFailed, err.Error())
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Validate MCPServers are ready
	if err := r.validateMCPServersReady(mcpServers); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentConditionMCPServersReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MCPServerNotReady",
			Message: err.Error(),
		})
		if patchErr := patchStatus(ctx, r.Client, agent, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Set MCPServersReady condition
	if len(agent.Spec.MCPServers) > 0 {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:   agentsv1alpha1.AgentConditionMCPServersReady,
			Status: metav1.ConditionTrue,
			Reason: "AllReady",
		})
	}

	// Set ProvidersReady condition
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionProvidersReady,
		Status:  metav1.ConditionTrue,
		Reason:  "Configured",
		Message: fmt.Sprintf("%d providers registered", len(agent.Spec.Providers)),
	})

	// Branch by mode
	var result ctrl.Result
	var reconcileErr error

	switch agent.Spec.Mode {
	case agentsv1alpha1.AgentModeDaemon:
		result, reconcileErr = r.reconcileDaemon(ctx, agent, mcpServers)
	case agentsv1alpha1.AgentModeTask:
		result, reconcileErr = r.reconcileTask(ctx, agent)
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

// reconcileDaemon handles daemon mode: PVC -> ConfigMaps -> Deployment -> Service -> NetworkPolicy -> status.
func (r *AgentReconciler) reconcileDaemon(ctx context.Context, agent *agentsv1alpha1.Agent, mcpServers []agentsv1alpha1.MCPServer) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. PVC
	if agent.Spec.Storage != nil {
		pvc := resources.BuildAgentPVC(agent)
		if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, pvc); err != nil {
			return ctrl.Result{}, err
		}
		agent.Status.StoragePVC = pvc.Name
	}

	// 2. Operator extension ConfigMap
	configMap, err := resources.BuildAgentConfigMap(agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, configMap); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Gateway ConfigMap (if MCP servers)
	if len(agent.Spec.MCPServers) > 0 {
		gwCM, err := resources.BuildGatewayConfigMap(agent)
		if err != nil {
			return ctrl.Result{}, err
		}
		if gwCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, gwCM); err != nil {
				return ctrl.Result{}, err
			}
		}

		// 4. MCP ConfigMap (mcp.json for pi-mcp-adapter)
		mcpCM, err := resources.BuildMCPConfigMap(agent)
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
	deployment := resources.BuildAgentDeployment(agent, mcpServers)
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

	// Tools loaded condition
	toolCount := len(agent.Spec.BuiltinTools) + len(agent.Spec.ToolRefs)
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionToolsLoaded,
		Status:  metav1.ConditionTrue,
		Reason:  "Loaded",
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
	return ctrl.Result{}, nil
}

// reconcileTask handles task mode: ConfigMaps -> status (Ready).
func (r *AgentReconciler) reconcileTask(ctx context.Context, agent *agentsv1alpha1.Agent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Operator extension ConfigMap
	configMap, err := resources.BuildAgentConfigMap(agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, configMap); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Gateway ConfigMap (if MCP servers)
	if len(agent.Spec.MCPServers) > 0 {
		gwCM, err := resources.BuildGatewayConfigMap(agent)
		if err != nil {
			return ctrl.Result{}, err
		}
		if gwCM != nil {
			if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, agent, gwCM); err != nil {
				return ctrl.Result{}, err
			}
		}

		mcpCM, err := resources.BuildMCPConfigMap(agent)
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

	toolCount := len(agent.Spec.BuiltinTools) + len(agent.Spec.ToolRefs)
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentConditionToolsLoaded,
		Status:  metav1.ConditionTrue,
		Reason:  "Loaded",
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

// resolveMCPServers fetches all MCPServer CRs referenced by the agent.
func (r *AgentReconciler) resolveMCPServers(ctx context.Context, agent *agentsv1alpha1.Agent) ([]agentsv1alpha1.MCPServer, error) {
	servers := make([]agentsv1alpha1.MCPServer, 0, len(agent.Spec.MCPServers))
	for _, binding := range agent.Spec.MCPServers {
		mcp := &agentsv1alpha1.MCPServer{}
		if err := r.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: agent.Namespace}, mcp); err != nil {
			return nil, fmt.Errorf("MCPServer %q not found: %w", binding.Name, err)
		}
		servers = append(servers, *mcp)
	}
	return servers, nil
}

// validateMCPServersReady checks all referenced MCPServers are in Ready phase.
func (r *AgentReconciler) validateMCPServersReady(servers []agentsv1alpha1.MCPServer) error {
	for _, mcp := range servers {
		if mcp.Status.Phase != agentsv1alpha1.MCPServerPhaseReady {
			return fmt.Errorf("MCPServer %q is not Ready (phase: %s)", mcp.Name, mcp.Status.Phase)
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
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Named("agent").
		Complete(r)
}
