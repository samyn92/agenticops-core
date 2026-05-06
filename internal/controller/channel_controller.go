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

// ChannelReconciler reconciles a Channel object.
type ChannelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=channels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=channels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=channels/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

func (r *ChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch Channel
	channel := &agentsv1alpha1.Channel{}
	if err := r.Get(ctx, req.NamespacedName, channel); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(channel.DeepCopy())

	log.Info("Reconciling Channel", "name", channel.Name, "type", channel.Spec.Type)

	// Resolve the target Agent
	agent := &agentsv1alpha1.Agent{}
	if err := r.Get(ctx, types.NamespacedName{Name: channel.Spec.AgentRef, Namespace: channel.Namespace}, agent); err != nil {
		r.setChannelFailedStatus(channel, fmt.Sprintf("Agent %q not found: %v", channel.Spec.AgentRef, err))
		if patchErr := patchStatus(ctx, r.Client, channel, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Surface any security override clamps before rendering the deployment.
	setSecurityPolicyViolationsCondition(&channel.Status.Conditions,
		resources.ComputeSecurityViolations(channel.Spec.Security))

	// 0. RBAC (ServiceAccount + Role + RoleBinding for AgentRun creation)
	sa := resources.BuildChannelServiceAccount(channel)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, sa); err != nil {
		return ctrl.Result{}, err
	}
	role := resources.BuildChannelRole(channel)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, role); err != nil {
		return ctrl.Result{}, err
	}
	rb := resources.BuildChannelRoleBinding(channel)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, rb); err != nil {
		return ctrl.Result{}, err
	}

	// 1. Deployment
	deployment := resources.BuildChannelDeployment(channel, agent)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, deployment); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Service
	service := resources.BuildChannelService(channel)
	if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, service); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Ingress (if webhook config set)
	ingress := resources.BuildChannelIngress(channel)
	if ingress != nil {
		if err := reconcileOwnedResource(ctx, r.Client, r.Scheme, channel, ingress); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 4. Compute status
	channel.Status.ServiceURL = fmt.Sprintf("http://%s.%s.svc:8080", channel.Name, channel.Namespace)

	if channel.Spec.Webhook != nil {
		scheme := "http"
		if channel.Spec.Webhook.TLS != nil {
			scheme = "https"
		}
		path := channel.Spec.Webhook.Path
		if path == "" {
			path = "/"
		}
		channel.Status.WebhookURL = fmt.Sprintf("%s://%s%s", scheme, channel.Spec.Webhook.Host, path)
	}

	// Check deployment readiness
	actualDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: channel.Name, Namespace: channel.Namespace}, actualDeploy); err == nil {
		if actualDeploy.Status.ReadyReplicas > 0 {
			channel.Status.Phase = agentsv1alpha1.ChannelPhaseReady
			meta.SetStatusCondition(&channel.Status.Conditions, metav1.Condition{
				Type:   agentsv1alpha1.ChannelConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "Ready",
			})
		} else {
			channel.Status.Phase = agentsv1alpha1.ChannelPhasePending
			meta.SetStatusCondition(&channel.Status.Conditions, metav1.Condition{
				Type:    agentsv1alpha1.ChannelConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "Pending",
				Message: "Waiting for deployment to be ready",
			})
		}
	}

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, channel, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Channel reconciled", "phase", channel.Status.Phase)

	// Requeue to poll for readiness since we filter Deployment status-only updates
	// via GenerationChangedPredicate.
	if channel.Status.Phase != agentsv1alpha1.ChannelPhaseReady {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// setChannelFailedStatus sets the Channel status to Failed. Caller must patch status.
func (r *ChannelReconciler) setChannelFailedStatus(ch *agentsv1alpha1.Channel, message string) {
	ch.Status.Phase = agentsv1alpha1.ChannelPhaseFailed
	meta.SetStatusCondition(&ch.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.ChannelConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.Channel{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&appsv1.Deployment{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Named("channel").
		Complete(r)
}
