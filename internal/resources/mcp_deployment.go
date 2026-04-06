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

package resources

import (
	"fmt"
	"sort"

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildMCPServerDeployment creates a Deployment for a deploy-mode MCPServer.
// The MCP server process is wrapped in mcp-gateway spawn mode.
func BuildMCPServerDeployment(mcp *agentsv1alpha1.MCPServer) *appsv1.Deployment {
	name := MCPServerObjectName(mcp.Name)
	port := mcp.Spec.Port
	if port == 0 {
		port = MCPServerDefaultPort
	}

	labels := map[string]string{
		LabelComponent: "mcp-server",
		LabelManagedBy: ManagedByValue,
		"app":          name,
	}

	var replicas int32 = 1

	// Build env vars from spec (sort map keys for deterministic order)
	envKeys := make([]string, 0, len(mcp.Spec.Env))
	for k := range mcp.Spec.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	env := make([]corev1.EnvVar, 0, len(mcp.Spec.Env)+len(mcp.Spec.Secrets)+2)
	for _, k := range envKeys {
		env = append(env, corev1.EnvVar{Name: k, Value: mcp.Spec.Env[k]})
	}
	for _, s := range mcp.Spec.Secrets {
		env = append(env, corev1.EnvVar{
			Name: s.Name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.SecretRef.Name},
					Key:                  s.SecretRef.Key,
				},
			},
		})
	}

	// Gateway spawn mode wrapping the MCP server process
	env = append(env,
		corev1.EnvVar{Name: "GATEWAY_MODE", Value: "spawn"},
		corev1.EnvVar{Name: "GATEWAY_PORT", Value: fmt.Sprintf("%d", port)},
	)

	container := corev1.Container{
		Name:  "mcp-server",
		Image: "ghcr.io/samyn92/mcp-gateway:latest",
		Env:   env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "mcp",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
	}

	if mcp.Spec.Resources != nil {
		container.Resources = *mcp.Spec.Resources
	}

	if len(mcp.Spec.Command) > 0 {
		container.Command = mcp.Spec.Command
	}

	// Health check
	if mcp.Spec.HealthCheck != nil && mcp.Spec.HealthCheck.Path != "" {
		interval := int32(30)
		if mcp.Spec.HealthCheck.IntervalSeconds > 0 {
			interval = int32(mcp.Spec.HealthCheck.IntervalSeconds)
		}
		container.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   mcp.Spec.HealthCheck.Path,
					Port:   intstr.FromInt32(port),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    interval,
			TimeoutSeconds:   1,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		}
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   mcp.Spec.HealthCheck.Path,
					Port:   intstr.FromInt32(port),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    interval,
			TimeoutSeconds:   1,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		}
	}

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
	}

	if mcp.Spec.ServiceAccountName != "" {
		podSpec.ServiceAccountName = mcp.Spec.ServiceAccountName
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcp.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}
