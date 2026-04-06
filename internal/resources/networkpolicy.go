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
	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildAgentNetworkPolicy creates a NetworkPolicy for a daemon agent.
// Restricts ingress to the agent runtime port only.
func BuildAgentNetworkPolicy(agent *agentsv1alpha1.Agent) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "netpol"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "networkpolicy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelAgent:     agent.Name,
					LabelComponent: "runtime",
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: portPtr(intstr.FromInt32(AgentRuntimePort)),
						},
					},
				},
			},
		},
	}
}

func portPtr(port intstr.IntOrString) *intstr.IntOrString {
	return &port
}
