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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildAgentRunJob creates a Job for a task-mode AgentRun.
func BuildAgentRunJob(run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent, mcpServers []agentsv1alpha1.MCPServer) *batchv1.Job {
	labels := CommonLabels(agent.Name, "task-run")
	labels["agents.agenticops.io/run"] = run.Name

	// Build the pod spec in task mode
	podSpec := buildAgentPodSpec(agent, mcpServers, true)

	// Inject AGENT_PROMPT into the main container
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == "agent-runtime" {
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
				Name:  "AGENT_PROMPT",
				Value: run.Spec.Prompt,
			})
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
				Name:  "AGENT_RUN_NAME",
				Value: run.Name,
			})
			break
		}
	}

	// Never restart task pods (they succeed or fail)
	podSpec.RestartPolicy = corev1.RestartPolicyNever

	var backoffLimit int32 = 0

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      run.Name,
			Namespace: run.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}
