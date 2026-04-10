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
	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentServiceAccountName returns the conventional ServiceAccount name for an agent.
func AgentServiceAccountName(agent *agentsv1alpha1.Agent) string {
	// If the user specified a custom SA, respect it (RBAC won't be managed).
	if agent.Spec.ServiceAccountName != "" {
		return agent.Spec.ServiceAccountName
	}
	return ObjectName(agent.Name, "agent")
}

// BuildAgentServiceAccount creates a ServiceAccount for the agent pod.
func BuildAgentServiceAccount(agent *agentsv1alpha1.Agent) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AgentServiceAccountName(agent),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "rbac"),
		},
	}
}

// BuildAgentRole creates a namespaced Role granting the agent pod
// permissions to manage AgentRun CRs, read Agent CRs, and read AgentResource CRs.
func BuildAgentRole(agent *agentsv1alpha1.Agent) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "agent"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "rbac"),
		},
		Rules: []rbacv1.PolicyRule{
			{
				// Allow creating and reading AgentRun CRs (run_agent + get_agent_run tools)
				APIGroups: []string{"agents.agentops.io"},
				Resources: []string{"agentruns"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				// Allow reading Agent CRs (for agent discovery / orchestration)
				APIGroups: []string{"agents.agentops.io"},
				Resources: []string{"agents"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				// Allow reading AgentResource CRs (list_task_agents resolves resource bindings)
				APIGroups: []string{"agents.agentops.io"},
				Resources: []string{"agentresources"},
				Verbs:     []string{"get", "list"},
			},
		},
	}
}

// BuildAgentRoleBinding creates a RoleBinding that binds the agent's
// ServiceAccount to its namespaced Role.
func BuildAgentRoleBinding(agent *agentsv1alpha1.Agent) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObjectName(agent.Name, "agent"),
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "rbac"),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     ObjectName(agent.Name, "agent"),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      AgentServiceAccountName(agent),
				Namespace: agent.Namespace,
			},
		},
	}
}
