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

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildAgentService creates a Service for a daemon agent.
func BuildAgentService(agent *agentsv1alpha1.Agent) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "service"),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				LabelAgent:     agent.Name,
				LabelComponent: "runtime",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       AgentRuntimePort,
					TargetPort: intstr.FromInt32(AgentRuntimePort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildMCPServerService creates a Service for a deployed MCPServer.
func BuildMCPServerService(mcp *agentsv1alpha1.MCPServer) *corev1.Service {
	name := MCPServerObjectName(mcp.Name)
	port := mcp.Spec.Port
	if port == 0 {
		port = MCPServerDefaultPort
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mcp.Namespace,
			Labels: map[string]string{
				LabelComponent: "mcp-server",
				LabelManagedBy: ManagedByValue,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				LabelComponent: "mcp-server",
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

// BuildChannelService creates a Service for a Channel bridge.
func BuildChannelService(ch *agentsv1alpha1.Channel) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ch.Name,
			Namespace: ch.Namespace,
			Labels: map[string]string{
				LabelComponent: "channel",
				LabelManagedBy: ManagedByValue,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				LabelComponent: "channel",
				"app":          ch.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt32(8080),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// AgentServiceURL returns the in-cluster URL for a daemon agent's service.
func AgentServiceURL(agent *agentsv1alpha1.Agent) string {
	return fmt.Sprintf("http://%s.%s.svc:%d", agent.Name, agent.Namespace, AgentRuntimePort)
}

// MCPServerServiceURL returns the in-cluster URL for an MCPServer's service.
func MCPServerServiceURL(mcp *agentsv1alpha1.MCPServer) string {
	name := MCPServerObjectName(mcp.Name)
	port := mcp.Spec.Port
	if port == 0 {
		port = MCPServerDefaultPort
	}
	return fmt.Sprintf("http://%s.%s.svc:%d", name, mcp.Namespace, port)
}
