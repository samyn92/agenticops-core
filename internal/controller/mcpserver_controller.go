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
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	"github.com/samyn92/agenticops-core/internal/resources"
)

// MCPServerReconciler reconciles an MCPServer object.
type MCPServerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=agents.agenticops.io,resources=mcpservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=mcpservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=mcpservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch MCPServer
	mcp := &agentsv1alpha1.MCPServer{}
	if err := r.Get(ctx, req.NamespacedName, mcp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(mcp.DeepCopy())

	log.Info("Reconciling MCPServer", "name", mcp.Name)

	var result ctrl.Result
	var reconcileErr error

	if mcp.Spec.Image != "" {
		result, reconcileErr = r.reconcileDeployMode(ctx, mcp)
	} else if mcp.Spec.URL != "" {
		result, reconcileErr = r.reconcileExternalMode(ctx, mcp)
	} else {
		r.setMCPFailedStatus(mcp, "Neither image nor url is set")
	}

	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, mcp, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

// reconcileDeployMode creates Deployment + Service + ConfigMap for an MCP server.
//
//nolint:unparam // Result is always nil for now but will be used for requeue logic.
func (r *MCPServerReconciler) reconcileDeployMode(ctx context.Context, mcp *agentsv1alpha1.MCPServer) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. ConfigMap
	cm := resources.BuildMCPServerConfigMap(mcp)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, mcp, cm); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Deployment
	deployment := resources.BuildMCPServerDeployment(mcp)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, mcp, deployment); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Service
	service := resources.BuildMCPServerService(mcp)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, mcp, service); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Check readiness and set status (caller will patch)
	name := resources.MCPServerObjectName(mcp.Name)
	actualDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: mcp.Namespace}, actualDeploy); err == nil {
		if actualDeploy.Status.ReadyReplicas > 0 {
			mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseReady
			meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
				Type:   agentsv1alpha1.MCPServerConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "Running",
			})
		} else {
			mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseDeploying
			meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
				Type:    agentsv1alpha1.MCPServerConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "Deploying",
				Message: "Waiting for deployment to be ready",
			})
		}
	}

	mcp.Status.ServiceURL = resources.MCPServerServiceURL(mcp)

	log.Info("MCPServer (deploy) reconciled", "phase", mcp.Status.Phase)
	return ctrl.Result{}, nil
}

// reconcileExternalMode validates and probes an external MCP server.
//
//nolint:unparam // error is always nil for now; health check failures set status, not error.
func (r *MCPServerReconciler) reconcileExternalMode(ctx context.Context, mcp *agentsv1alpha1.MCPServer) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	mcp.Status.ServiceURL = mcp.Spec.URL

	// Health probe
	healthURL := mcp.Spec.URL
	if mcp.Spec.HealthCheck != nil && mcp.Spec.HealthCheck.URL != "" {
		healthURL = mcp.Spec.HealthCheck.URL
	}

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := httpClient.Get(healthURL)
	if err != nil {
		mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseFailed
		meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.MCPServerConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "HealthCheckFailed",
			Message: fmt.Sprintf("Failed to reach %s: %v", healthURL, err),
		})
	} else {
		resp.Body.Close()
		if resp.StatusCode < 400 {
			mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseReady
			meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
				Type:   agentsv1alpha1.MCPServerConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "Reachable",
			})
		} else {
			mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseFailed
			meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
				Type:    agentsv1alpha1.MCPServerConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "HealthCheckFailed",
				Message: fmt.Sprintf("Health check returned %d", resp.StatusCode),
			})
		}
	}

	// Periodic re-check for external servers (caller will patch status)
	interval := 30 * time.Second
	if mcp.Spec.HealthCheck != nil && mcp.Spec.HealthCheck.IntervalSeconds > 0 {
		interval = time.Duration(mcp.Spec.HealthCheck.IntervalSeconds) * time.Second
	}

	log.Info("MCPServer (external) reconciled", "phase", mcp.Status.Phase)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// setMCPFailedStatus sets the MCPServer status to Failed. Caller must patch status.
func (r *MCPServerReconciler) setMCPFailedStatus(mcp *agentsv1alpha1.MCPServer, message string) {
	mcp.Status.Phase = agentsv1alpha1.MCPServerPhaseFailed
	meta.SetStatusCondition(&mcp.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.MCPServerConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.MCPServer{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("mcpserver").
		Complete(r)
}
