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
	"time"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildAgentRunJob creates a Job for a task-mode AgentRun.
// gitCfg is optional — when non-nil, git workspace env vars and tool init containers are injected.
// runConfigMapName overrides the operator config volume to use a per-run ConfigMap (empty = use agent default).
func BuildAgentRunJob(run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, providers []agentsv1alpha1.Provider, gitCfg *GitWorkspaceConfig, runConfigMapName string) *batchv1.Job {
	labels := CommonLabels(agent.Name, "task-run")
	labels["agents.agentops.io/run"] = run.Name

	// Build the pod spec in task mode
	podSpec := buildAgentPodSpec(agent, agentTools, providers, true)

	// Inject AGENT_PROMPT and AGENT_RUN_NAME into the main container
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == ContainerRuntime {
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
				Name:  "AGENT_PROMPT",
				Value: run.Spec.Prompt,
			})
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
				Name:  "AGENT_RUN_NAME",
				Value: run.Name,
			})

			// Inject trace context for cross-agent trace linking.
			// The runtime reads TRACEPARENT to create a span link back to the parent agent.
			if tp, ok := run.Annotations["agents.agentops.io/traceparent"]; ok && tp != "" {
				podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
					Name:  "TRACEPARENT",
					Value: tp,
				})
			}
			if parentAgent, ok := run.Annotations["agents.agentops.io/parent-agent"]; ok && parentAgent != "" {
				podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{
					Name:  "AGENT_RUN_SOURCE_AGENT",
					Value: parentAgent,
				})
			}

			// Inject git workspace env vars and tool volume mounts
			if gitCfg != nil {
				podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, gitCfg.GitEnvVars()...)
				podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts, gitCfg.GitToolVolumeMounts()...)
			}
			break
		}
	}

	// Inject git tool init containers and volumes (no sidecars — tools run via stdio).
	// Init containers are appended after buildAgentPodSpec has already hardened
	// the existing ones; re-apply ApplySecurity so the new ones get the same
	// restricted SecurityContext + /tmp mount.
	if gitCfg != nil {
		podSpec.InitContainers = append(podSpec.InitContainers, gitCfg.GitToolInitContainers()...)
		podSpec.Volumes = append(podSpec.Volumes, gitCfg.GitToolVolumes()...)
		ApplySecurity(&podSpec, ContainerRuntime, agent.Spec.Security)
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

	// Parse timeout from agent spec → ActiveDeadlineSeconds to prevent infinite jobs.
	var activeDeadline *int64
	if agent.Spec.Timeout != "" {
		if d, err := time.ParseDuration(agent.Spec.Timeout); err == nil && d > 0 {
			secs := int64(d.Seconds())
			activeDeadline = &secs
		}
	}
	if activeDeadline == nil {
		// Default: 30 minutes for task jobs.
		defaultSecs := int64(1800)
		activeDeadline = &defaultSecs
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      run.Name,
			Namespace: run.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: activeDeadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}
