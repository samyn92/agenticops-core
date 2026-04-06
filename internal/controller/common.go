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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// requeueInterval is the default requeue interval for controllers waiting on async work.
	requeueInterval = 10 * time.Second
	// fieldManager is the SSA field manager name for the operator.
	fieldManager = "agenticops-operator"
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
	log := ctrl.LoggerFrom(ctx)

	switch d := desired.(type) {
	case *appsv1.Deployment:
		// Use Server-Side Apply for Deployments. SSA only manages fields
		// explicitly set in the apply configuration, so API-server defaults
		// (imagePullPolicy, terminationMessagePath, etc.) are never touched.
		if err := controllerutil.SetControllerReference(owner, d, scheme); err != nil {
			return fmt.Errorf("set owner ref on Deployment %s: %w", key, err)
		}
		// SSA requires GVK to be set on the object.
		d.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
		if err := c.Patch(ctx, d, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
			return fmt.Errorf("apply Deployment %s: %w", key, err)
		}
		log.V(1).Info("Applied Deployment (SSA)", "name", key.Name)
		return nil

	case *corev1.Service:
		existing := &corev1.Service{}
		existing.Name = key.Name
		existing.Namespace = key.Namespace
		result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
			if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
				return err
			}
			existing.Labels = mergeLabels(existing.Labels, d.Labels)
			existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)

			// Only update the fields we manage — preserve ClusterIP, SessionAffinity,
			// IPFamilyPolicy, InternalTrafficPolicy and other API-server defaults.
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
		log.V(1).Info("CreateOrUpdate Service", "name", key.Name, "result", result)
		return nil

	case *corev1.ConfigMap:
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
		log.V(1).Info("CreateOrUpdate ConfigMap", "name", key.Name, "result", result)
		return nil

	case *corev1.PersistentVolumeClaim:
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

	case *networkingv1.NetworkPolicy:
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

	case *networkingv1.Ingress:
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

	default:
		return fmt.Errorf("unsupported resource type %T", desired)
	}
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
