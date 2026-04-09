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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildAgentRunJob creates a Job for a task-mode AgentRun.
// gitCfg is optional — when non-nil, git workspace env vars and MCP tool sidecars are injected.
// runConfigMapName overrides the operator config volume to use a per-run ConfigMap (empty = use agent default).
func BuildAgentRunJob(run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, gitCfg *GitWorkspaceConfig, runConfigMapName string) *batchv1.Job {
	labels := CommonLabels(agent.Name, "task-run")
	labels["agents.agentops.io/run"] = run.Name

	// Build the pod spec in task mode
	podSpec := buildAgentPodSpec(agent, agentTools, true)

	// Inject AGENT_PROMPT and AGENT_RUN_NAME into the main container
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

			// Inject git workspace env vars
			if gitCfg != nil {
				podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, gitCfg.GitEnvVars()...)
			}
			break
		}
	}

	// Inject git MCP tool sidecars, init containers, and volumes
	if gitCfg != nil {
		// Count existing MCP sidecars to offset port allocation
		mcpCount := 0
		for _, c := range podSpec.Containers {
			if c.Name != "agent-runtime" {
				mcpCount++
			}
		}
		podSpec.Containers = append(podSpec.Containers, gitCfg.GitToolSidecars(mcpCount)...)
		podSpec.InitContainers = append(podSpec.InitContainers, gitCfg.GitToolInitContainers()...)
		podSpec.Volumes = append(podSpec.Volumes, gitCfg.GitToolVolumes()...)
	}

	// Override the config volume to use a per-run ConfigMap if specified
	if runConfigMapName != "" {
		for i, vol := range podSpec.Volumes {
			if vol.Name == VolumeConfig && vol.ConfigMap != nil {
				podSpec.Volumes[i].ConfigMap.Name = runConfigMapName
				break
			}
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
