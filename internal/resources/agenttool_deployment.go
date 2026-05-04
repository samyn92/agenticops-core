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

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// AgentToolObjectName returns the conventional name for AgentTool sub-resources.
func AgentToolObjectName(toolName string) string {
	return fmt.Sprintf("agtool-%s", toolName)
}

// AgentToolServiceURL returns the in-cluster URL for an AgentTool's mcpServer service.
func AgentToolServiceURL(tool *agentsv1alpha1.AgentTool) string {
	if tool.Spec.MCPEndpoint != nil {
		return tool.Spec.MCPEndpoint.URL
	}
	if tool.Spec.MCPServer != nil {
		name := AgentToolObjectName(tool.Name)
		port := tool.Spec.MCPServer.Port
		if port == 0 {
			port = MCPServerDefaultPort
		}
		return fmt.Sprintf("http://%s.%s.svc:%d", name, tool.Namespace, port)
	}
	return ""
}

// BuildAgentToolDeployment creates a Deployment for an AgentTool with mcpServer source.
// The MCP server process is wrapped in mcp-gateway spawn mode.
func BuildAgentToolDeployment(tool *agentsv1alpha1.AgentTool) *appsv1.Deployment {
	src := tool.Spec.MCPServer
	name := AgentToolObjectName(tool.Name)
	port := src.Port
	if port == 0 {
		port = MCPServerDefaultPort
	}

	labels := map[string]string{
		LabelComponent: "agenttool",
		LabelManagedBy: ManagedByValue,
		"app":          name,
	}

	var replicas int32 = 1

	// Build env vars from spec (sort map keys for deterministic order)
	envKeys := make([]string, 0, len(src.Env))
	for k := range src.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	env := make([]corev1.EnvVar, 0, len(src.Env)+len(src.Secrets)+2)
	for _, k := range envKeys {
		env = append(env, corev1.EnvVar{Name: k, Value: src.Env[k]})
	}
	for _, s := range src.Secrets {
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

	// Gateway spawn mode
	gatewayImage := MCPGatewayImage()
	gatewayVolume := "gateway-bin"

	env = append(env,
		corev1.EnvVar{Name: "GATEWAY_MODE", Value: "spawn"},
		corev1.EnvVar{Name: "GATEWAY_PORT", Value: fmt.Sprintf("%d", port)},
	)

	if len(src.Command) > 0 {
		cmdParts := make([]string, 0, len(src.Command))
		cmdParts = append(cmdParts, src.Command...)
		env = append(env, corev1.EnvVar{
			Name:  "GATEWAY_COMMAND",
			Value: joinCommand(cmdParts),
		})
	}

	container := corev1.Container{
		Name:    "mcp-server",
		Image:   src.Image,
		Command: []string{"/gateway/mcp-gateway"},
		Env:     env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "mcp",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      gatewayVolume,
				MountPath: "/gateway",
				ReadOnly:  true,
			},
		},
	}

	if src.Resources != nil {
		container.Resources = *src.Resources
	}

	// Health check
	if src.HealthCheck != nil && src.HealthCheck.Path != "" {
		interval := int32(30)
		if src.HealthCheck.IntervalSeconds > 0 {
			interval = int32(src.HealthCheck.IntervalSeconds)
		}
		container.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   src.HealthCheck.Path,
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
					Path:   src.HealthCheck.Path,
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

	// Init container copies the mcp-gateway binary
	initContainer := corev1.Container{
		Name:    "copy-gateway",
		Image:   gatewayImage,
		Command: []string{"/mcp-gateway"},
		Args:    []string{"--copy-to=/gateway/mcp-gateway"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      gatewayVolume,
				MountPath: "/gateway",
			},
		},
	}

	podSpec := corev1.PodSpec{
		InitContainers: []corev1.Container{initContainer},
		Containers:     []corev1.Container{container},
		Volumes: []corev1.Volume{
			{
				Name: gatewayVolume,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	if src.ServiceAccountName != "" {
		podSpec.ServiceAccountName = src.ServiceAccountName
	}

	// Restricted-by-default security on the AgentTool MCP server pod.
	ApplySecurity(&podSpec, "mcp-server", src.Security)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
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

// BuildAgentToolService creates a Service for an AgentTool with mcpServer source.
func BuildAgentToolService(tool *agentsv1alpha1.AgentTool) *corev1.Service {
	name := AgentToolObjectName(tool.Name)
	port := tool.Spec.MCPServer.Port
	if port == 0 {
		port = MCPServerDefaultPort
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels: map[string]string{
				LabelComponent: "agenttool",
				LabelManagedBy: ManagedByValue,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				LabelComponent: "agenttool",
				"app":          name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "mcp",
					Port:       port,
					TargetPort: intstr.FromInt32(port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildAgentToolMCPConfigMap generates a ConfigMap for an AgentTool mcpServer deployment.
func BuildAgentToolMCPConfigMap(tool *agentsv1alpha1.AgentTool) *corev1.ConfigMap {
	name := AgentToolObjectName(tool.Name)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-config", name),
			Namespace: tool.Namespace,
			Labels: map[string]string{
				LabelComponent: "agenttool",
				LabelManagedBy: ManagedByValue,
			},
		},
		Data: map[string]string{
			"port": fmt.Sprintf("%d", tool.Spec.MCPServer.Port),
		},
	}
}

// BuildAgentToolInlineConfigMap creates a ConfigMap storing inline tool content.
func BuildAgentToolInlineConfigMap(tool *agentsv1alpha1.AgentTool) *corev1.ConfigMap {
	name := fmt.Sprintf("%s-inline", AgentToolObjectName(tool.Name))

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels: map[string]string{
				LabelComponent: "agenttool",
				LabelManagedBy: ManagedByValue,
			},
		},
		Data: map[string]string{
			"tool": tool.Spec.Inline.Content,
		},
	}
}
