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
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// AgentToolReconciler reconciles an AgentTool object.
type AgentToolReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=agenttools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agenttools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agenttools/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *AgentToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch AgentTool
	tool := &agentsv1alpha1.AgentTool{}
	if err := r.Get(ctx, req.NamespacedName, tool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(tool.DeepCopy())

	log.Info("Reconciling AgentTool", "name", tool.Name)

	// Detect and record source type
	tool.Status.SourceType = tool.DetectSourceType()

	var result ctrl.Result
	var reconcileErr error

	switch {
	case tool.Spec.OCI != nil:
		result, reconcileErr = r.reconcileOCI(ctx, tool)
	case tool.Spec.ConfigMap != nil:
		result, reconcileErr = r.reconcileConfigMap(ctx, tool)
	case tool.Spec.Inline != nil:
		result, reconcileErr = r.reconcileInline(ctx, tool)
	case tool.Spec.MCPServer != nil:
		result, reconcileErr = r.reconcileMCPServer(ctx, tool)
	case tool.Spec.MCPEndpoint != nil:
		result, reconcileErr = r.reconcileMCPEndpoint(ctx, tool)
	case tool.Spec.Skill != nil:
		result, reconcileErr = r.reconcileSkill(ctx, tool)
	default:
		r.setFailedStatus(tool, "No source block set")
	}

	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, tool, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

// reconcileOCI validates an OCI tool source and marks it Ready.
// No K8s resources are created — init containers are an agent-side concern.
//
//nolint:unparam
func (r *AgentToolReconciler) reconcileOCI(_ context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	// OCI tools are validated at webhook time. At reconcile time we just
	// mark them Ready. A future enhancement could verify the image exists
	// via crane head.
	tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentToolConditionReady,
		Status: metav1.ConditionTrue,
		Reason: "OCIRefValid",
	})
	return ctrl.Result{}, nil
}

// reconcileConfigMap verifies the referenced ConfigMap exists and marks Ready.
//
//nolint:unparam
func (r *AgentToolReconciler) reconcileConfigMap(ctx context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Name:      tool.Spec.ConfigMap.Name,
		Namespace: tool.Namespace,
	}
	if err := r.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			r.setFailedStatus(tool, fmt.Sprintf("ConfigMap %q not found", tool.Spec.ConfigMap.Name))
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		return ctrl.Result{}, err
	}

	if _, ok := cm.Data[tool.Spec.ConfigMap.Key]; !ok {
		r.setFailedStatus(tool, fmt.Sprintf("key %q not found in ConfigMap %q",
			tool.Spec.ConfigMap.Key, tool.Spec.ConfigMap.Name))
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentToolConditionReady,
		Status: metav1.ConditionTrue,
		Reason: "ConfigMapFound",
	})
	return ctrl.Result{}, nil
}

// reconcileInline creates/updates a ConfigMap with the inline content.
//
//nolint:unparam
func (r *AgentToolReconciler) reconcileInline(ctx context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	cm := resources.BuildAgentToolInlineConfigMap(tool)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, tool, cm); err != nil {
		return ctrl.Result{}, err
	}

	tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentToolConditionReady,
		Status: metav1.ConditionTrue,
		Reason: "InlineContentStored",
	})
	return ctrl.Result{}, nil
}

// reconcileMCPServer creates Deployment + Service + ConfigMap for a deployed MCP server.
// This is a direct lift of the MCPServerReconciler deploy mode logic.
func (r *AgentToolReconciler) reconcileMCPServer(ctx context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Surface any security override clamps before we render the deployment.
	setSecurityPolicyViolationsCondition(&tool.Status.Conditions,
		resources.ComputeSecurityViolations(tool.Spec.MCPServer.Security))

	// 1. ConfigMap
	cm := resources.BuildAgentToolMCPConfigMap(tool)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, tool, cm); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Deployment
	deployment := resources.BuildAgentToolDeployment(tool)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, tool, deployment); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Service
	service := resources.BuildAgentToolService(tool)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, tool, service); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Check readiness and set status
	name := resources.AgentToolObjectName(tool.Name)
	actualDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: tool.Namespace}, actualDeploy); err == nil {
		if actualDeploy.Status.ReadyReplicas > 0 {
			tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:   agentsv1alpha1.AgentToolConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "Running",
			})
		} else {
			tool.Status.Phase = agentsv1alpha1.AgentToolPhaseDeploying
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:    agentsv1alpha1.AgentToolConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "Deploying",
				Message: "Waiting for deployment to be ready",
			})
		}
	}

	tool.Status.ServiceURL = resources.AgentToolServiceURL(tool)

	log.Info("AgentTool (mcpServer) reconciled", "phase", tool.Status.Phase)

	if tool.Status.Phase != agentsv1alpha1.AgentToolPhaseReady {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// reconcileMCPEndpoint probes an external MCP endpoint and sets status.
// This is a direct lift of the MCPServerReconciler external mode logic.
//
//nolint:unparam
func (r *AgentToolReconciler) reconcileMCPEndpoint(ctx context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	tool.Status.ServiceURL = tool.Spec.MCPEndpoint.URL

	// Health probe
	healthURL := tool.Spec.MCPEndpoint.URL
	if tool.Spec.MCPEndpoint.HealthCheck != nil && tool.Spec.MCPEndpoint.HealthCheck.URL != "" {
		healthURL = tool.Spec.MCPEndpoint.HealthCheck.URL
	}

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := httpClient.Get(healthURL)
	if err != nil {
		tool.Status.Phase = agentsv1alpha1.AgentToolPhaseFailed
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentToolConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "HealthCheckFailed",
			Message: fmt.Sprintf("Failed to reach %s: %v", healthURL, err),
		})
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:   agentsv1alpha1.AgentToolConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "Reachable",
			})
		} else {
			tool.Status.Phase = agentsv1alpha1.AgentToolPhaseFailed
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:    agentsv1alpha1.AgentToolConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "HealthCheckFailed",
				Message: fmt.Sprintf("Health check returned %d", resp.StatusCode),
			})
		}
	}

	// Periodic re-check
	interval := 30 * time.Second
	if tool.Spec.MCPEndpoint.HealthCheck != nil && tool.Spec.MCPEndpoint.HealthCheck.IntervalSeconds > 0 {
		interval = time.Duration(tool.Spec.MCPEndpoint.HealthCheck.IntervalSeconds) * time.Second
	}

	log.Info("AgentTool (mcpEndpoint) reconciled", "phase", tool.Status.Phase)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// reconcileSkill validates a skill OCI source and marks it Ready.
// No K8s resources are created — init containers are an agent-side concern.
//
//nolint:unparam
func (r *AgentToolReconciler) reconcileSkill(_ context.Context, tool *agentsv1alpha1.AgentTool) (ctrl.Result, error) {
	tool.Status.Phase = agentsv1alpha1.AgentToolPhaseReady
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentToolConditionReady,
		Status: metav1.ConditionTrue,
		Reason: "SkillRefValid",
	})
	return ctrl.Result{}, nil
}

// setFailedStatus sets the AgentTool status to Failed. Caller must patch status.
func (r *AgentToolReconciler) setFailedStatus(tool *agentsv1alpha1.AgentTool, message string) {
	tool.Status.Phase = agentsv1alpha1.AgentToolPhaseFailed
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentToolConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentToolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.AgentTool{}).
		Owns(&appsv1.Deployment{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("agenttool").
		Complete(r)
}
