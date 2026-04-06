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
)

// reconcileOwnedResource creates or updates a child resource using controllerutil.CreateOrUpdate.
// It only triggers an API write if the mutate function actually changes the object,
// preventing infinite reconciliation loops from owned-resource watches.
//
// IMPORTANT: The mutate function must only set fields the operator manages.
// It must NOT replace entire sub-structs (e.g. Spec.Template, Spec) because the
// API server adds defaulted fields (DNSPolicy, RestartPolicy, imagePullPolicy, etc.)
// that would be wiped, causing a diff on every reconcile → infinite loop.
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
		existing := &appsv1.Deployment{}
		existing.Name = key.Name
		existing.Namespace = key.Namespace
		result, err := controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
			if err := controllerutil.SetControllerReference(owner, existing, scheme); err != nil {
				return err
			}
			// Labels & annotations: merge desired into existing (don't clobber API-added labels)
			existing.Labels = mergeLabels(existing.Labels, d.Labels)
			existing.Annotations = mergeLabels(existing.Annotations, d.Annotations)

			// Deployment-level spec: only replicas and selector (both are ours)
			existing.Spec.Replicas = d.Spec.Replicas
			existing.Spec.Selector = d.Spec.Selector

			// Pod template metadata: merge labels (API server doesn't add any, but be safe)
			existing.Spec.Template.Labels = mergeLabels(existing.Spec.Template.Labels, d.Spec.Template.Labels)
			existing.Spec.Template.Annotations = mergeLabels(existing.Spec.Template.Annotations, d.Spec.Template.Annotations)

			// Pod spec: only update the fields we control, preserve API-server defaults
			// (DNSPolicy, RestartPolicy, SchedulerName, TerminationGracePeriodSeconds, SecurityContext, etc.)
			desiredPodSpec := &d.Spec.Template.Spec
			existingPodSpec := &existing.Spec.Template.Spec

			existingPodSpec.ServiceAccountName = desiredPodSpec.ServiceAccountName
			existingPodSpec.InitContainers = desiredPodSpec.InitContainers
			existingPodSpec.Volumes = desiredPodSpec.Volumes

			// Merge containers: match by name, update image/env/ports/resources/command/volumeMounts/probes
			existingPodSpec.Containers = mergeContainers(existingPodSpec.Containers, desiredPodSpec.Containers)

			return nil
		})
		if err != nil {
			return fmt.Errorf("reconcile Deployment %s: %w", key, err)
		}
		log.V(1).Info("CreateOrUpdate Deployment", "name", key.Name, "result", result)
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

// mergeContainers updates existing containers with desired values, matched by name.
// Only fields the operator manages are updated; API-server defaults (imagePullPolicy,
// terminationMessagePath, terminationMessagePolicy, probe thresholds, etc.) are preserved.
func mergeContainers(existing, desired []corev1.Container) []corev1.Container {
	if len(existing) == 0 {
		return desired
	}

	existingByName := make(map[string]int, len(existing))
	for i := range existing {
		existingByName[existing[i].Name] = i
	}

	for _, dc := range desired {
		idx, found := existingByName[dc.Name]
		if !found {
			// New container — append
			existing = append(existing, dc)
			continue
		}
		ec := &existing[idx]

		// Update only operator-managed fields
		ec.Image = dc.Image
		ec.Command = dc.Command
		ec.Args = dc.Args
		ec.Env = dc.Env
		ec.EnvFrom = dc.EnvFrom
		ec.Ports = dc.Ports
		ec.VolumeMounts = dc.VolumeMounts
		if dc.Resources.Limits != nil || dc.Resources.Requests != nil {
			ec.Resources = dc.Resources
		}

		// Probes: only update if desired specifies them (don't nil out existing probes)
		if dc.LivenessProbe != nil {
			ec.LivenessProbe = dc.LivenessProbe
		}
		if dc.ReadinessProbe != nil {
			ec.ReadinessProbe = dc.ReadinessProbe
		}
		if dc.StartupProbe != nil {
			ec.StartupProbe = dc.StartupProbe
		}
	}

	// Remove containers not in desired (operator removed them)
	desiredNames := make(map[string]bool, len(desired))
	for _, dc := range desired {
		desiredNames[dc.Name] = true
	}
	filtered := existing[:0]
	for _, ec := range existing {
		if desiredNames[ec.Name] {
			filtered = append(filtered, ec)
		}
	}
	return filtered
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
