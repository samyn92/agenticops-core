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
	"strings"
	"time"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// requeueInterval is the default requeue interval for controllers waiting on async work.
	requeueInterval = 10 * time.Second
	// fieldManager is the SSA field manager name for the operator.
	fieldManager = "agentops-operator"
)

// reconcileOwnedResource creates or updates a child resource.
//
// For Deployments, it uses Server-Side Apply (SSA) to avoid infinite
// reconciliation loops caused by API-server-defaulted fields.
//
// For other resource types, it uses controllerutil.CreateOrUpdate with
// careful field-by-field merging to preserve API-server defaults.
func reconcileOwnedResource(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	desired client.Object,
) error {
	key := client.ObjectKeyFromObject(desired)

	switch d := desired.(type) {
	case *appsv1.Deployment:
		return reconcileDeploymentSSA(ctx, c, scheme, owner, d, key)

	case *corev1.Service:
		return reconcileService(ctx, c, scheme, owner, d, key)

	case *corev1.ConfigMap:
		return reconcileConfigMap(ctx, c, scheme, owner, d, key)

	case *corev1.PersistentVolumeClaim:
		return reconcilePVC(ctx, c, scheme, owner, d, key)

	case *networkingv1.NetworkPolicy:
		return reconcileNetworkPolicy(ctx, c, scheme, owner, d, key)

	case *networkingv1.Ingress:
		return reconcileIngress(ctx, c, scheme, owner, d, key)

	case *corev1.ServiceAccount:
		return reconcileServiceAccount(ctx, c, scheme, owner, d, key)

	case *rbacv1.Role:
		return reconcileRole(ctx, c, scheme, owner, d, key)

	case *rbacv1.RoleBinding:
		return reconcileRoleBinding(ctx, c, scheme, owner, d, key)

	default:
		return fmt.Errorf("unsupported resource type %T", desired)
	}
}

// reconcileDeploymentSSA applies a Deployment using Server-Side Apply.
// SSA only manages fields explicitly set in the apply configuration, so
// API-server defaults (imagePullPolicy, terminationMessagePath, etc.) are never touched.
func reconcileDeploymentSSA(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *appsv1.Deployment, key client.ObjectKey,
) error {
	if err := controllerutil.SetControllerReference(owner, d, scheme); err != nil {
		return fmt.Errorf("set owner ref on Deployment %s: %w", key, err)
	}
	d.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	if err := c.Patch(ctx, d, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil { //nolint:staticcheck // migrate to c.Apply() with applyconfigurations later
		return fmt.Errorf("apply Deployment %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("Applied Deployment (SSA)", "name", key.Name)
	return nil
}

func reconcileService(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *corev1.Service, key client.ObjectKey,
) error {
	existing := &corev1.Service{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		existing.Spec.Selector = d.Spec.Selector
		existing.Spec.Ports = d.Spec.Ports
		if d.Spec.Type != "" {
			existing.Spec.Type = d.Spec.Type
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile Service %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("CreateOrUpdate Service", "name", key.Name, "result", result)
	return nil
}

func reconcileConfigMap(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *corev1.ConfigMap, key client.ObjectKey,
) error {
	existing := &corev1.ConfigMap{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		existing.Data = d.Data
		existing.BinaryData = d.BinaryData
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile ConfigMap %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("CreateOrUpdate ConfigMap", "name", key.Name, "result", result)
	return nil
}

func reconcilePVC(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *corev1.PersistentVolumeClaim, key client.ObjectKey,
) error {
	existing := &corev1.PersistentVolumeClaim{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		// PVC spec is mostly immutable after creation; only update labels/annotations
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = d.Spec
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile PVC %s: %w", key, err)
	}
	return nil
}

func reconcileNetworkPolicy(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *networkingv1.NetworkPolicy, key client.ObjectKey,
) error {
	existing := &networkingv1.NetworkPolicy{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		existing.Spec = d.Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func reconcileIngress(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *networkingv1.Ingress, key client.ObjectKey,
) error {
	existing := &networkingv1.Ingress{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		existing.Spec = d.Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile Ingress %s: %w", key, err)
	}
	return nil
}

func reconcileServiceAccount(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *corev1.ServiceAccount, key client.ObjectKey,
) error {
	existing := &corev1.ServiceAccount{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile ServiceAccount %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("CreateOrUpdate ServiceAccount", "name", key.Name, "result", result)
	return nil
}

func reconcileRole(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *rbacv1.Role, key client.ObjectKey,
) error {
	existing := &rbacv1.Role{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		existing.Rules = d.Rules
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile Role %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("CreateOrUpdate Role", "name", key.Name, "result", result)
	return nil
}

func reconcileRoleBinding(
	ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner client.Object, d *rbacv1.RoleBinding, key client.ObjectKey,
) error {
	existing := &rbacv1.RoleBinding{}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
			return err
		}
		existing.Labels = mergeLabels(existing.Labels, d.Labels)
		existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)
		// RoleRef is immutable after creation; only set if new
		if existing.CreationTimestamp.IsZero() {
			existing.RoleRef = d.RoleRef
		}
		existing.Subjects = d.Subjects
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile RoleBinding %s: %w", key, err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("CreateOrUpdate RoleBinding", "name", key.Name, "result", result)
	return nil
}

// mergeLabels merges desired labels into existing labels. Desired keys win.
// Returns desired if existing is nil, preserving any extra keys from the API server.
func mergeLabels(existing, desired map[string]string) map[string]string {
	if len(desired) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return desired
	}
	merged := make(map[string]string, len(existing)+len(desired))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range desired {
		merged[k] = v
	}
	return merged
}

// patchStatus patches the status subresource only if it has changed.
// It compares the current status against the original (before modifications),
// and only sends the patch if there's a difference. This prevents
// infinite reconciliation loops caused by no-op status updates.
func patchStatus(ctx context.Context, c client.Client, obj client.Object, patch client.Patch) error {
	// MergeFrom patches: compute the patch data. If empty (no diff), skip the API call.
	patchData, err := patch.Data(obj)
	if err != nil {
		return fmt.Errorf("compute status patch: %w", err)
	}

	// A JSON merge patch with no changes produces "{}" or just the status key with no diff.
	// If the patch is just "{}" (2 bytes), there's nothing to update.
	if len(patchData) <= 2 || string(patchData) == "{}" {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Patching status", "patch", string(patchData), "patchLen", len(patchData))

	return c.Status().Patch(ctx, obj, patch)
}

// setSecurityPolicyViolationsCondition records the result of merging
// user-supplied SecurityOverrides with the operator's restricted-PSS floor.
// When the slice is empty, an explicit ConditionFalse is set so the user can
// see the operator has confirmed their overrides are clean. When non-empty,
// the condition becomes True and the message lists every clamped field.
func setSecurityPolicyViolationsCondition(conds *[]metav1.Condition, violations []string) {
	if len(violations) == 0 {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    agentsv1alpha1.ConditionSecurityPolicyViolations,
			Status:  metav1.ConditionFalse,
			Reason:  "NoViolations",
			Message: "Security overrides comply with the restricted Pod Security Standard.",
		})
		return
	}
	msg := fmt.Sprintf("%d override field(s) clamped to the restricted-PSS floor: %s",
		len(violations), strings.Join(violations, "; "))
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    agentsv1alpha1.ConditionSecurityPolicyViolations,
		Status:  metav1.ConditionTrue,
		Reason:  "OverridesClamped",
		Message: msg,
	})
}
