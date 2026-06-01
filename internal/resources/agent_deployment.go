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

// BuildAgentDeployment creates the Deployment for a daemon agent.
func BuildAgentDeployment(agent *agentsv1alpha1.Agent, providers []agentsv1alpha1.Provider, integrations []agentsv1alpha1.Integration, infra InfraConfig) *appsv1.Deployment {
	labels := CommonLabels(agent.Name, "runtime")
	var replicas int32 = 1

	// Build pod spec
	podSpec := buildAgentPodSpec(agent, providers, integrations, false, infra)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agent.Name,
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelAgent:     agent.Name,
					LabelComponent: "runtime",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: agent.Spec.PodAnnotations,
				},
				Spec: podSpec,
			},
		},
	}

	// Use Recreate strategy when a PVC is attached — RWO volumes can only
	// be mounted by one pod at a time, so RollingUpdate would deadlock.
	if agent.Spec.Storage != nil {
		deployment.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		}
	}

	return deployment
}

// buildAgentPodSpec creates the complete PodSpec for daemon or task mode.
// taskMode=true uses emptyDir for /data instead of PVC.
// integrations are the agent's resolved Integration CRs (used to wire a bound
// GitLab identity into daemon agents); pass nil for task mode.
func buildAgentPodSpec(agent *agentsv1alpha1.Agent, providers []agentsv1alpha1.Provider, integrations []agentsv1alpha1.Integration, taskMode bool, infra InfraConfig) corev1.PodSpec {
	// Volumes
	volumes := buildVolumes(agent, taskMode)

	// Init containers: OCI tool pulls
	initContainers := buildInitContainers(agent)

	// Main container
	mainContainer := buildMainContainer(agent, providers, integrations, taskMode, infra)

	// No sidecars — all tools are OCI/stdio, no gateway needed.
	containers := []corev1.Container{mainContainer}

	podSpec := corev1.PodSpec{
		InitContainers:     initContainers,
		Containers:         containers,
		Volumes:            volumes,
		ServiceAccountName: AgentServiceAccountName(agent),
	}

	// Apply restricted-by-default security on every container/init/sidecar
	// and the pod itself, merged with any user-supplied overrides.
	ApplySecurity(&podSpec, ContainerRuntime, agent.Spec.Security)

	return podSpec
}

func buildVolumes(agent *agentsv1alpha1.Agent, taskMode bool) []corev1.Volume {
	volumes := []corev1.Volume{
		// Tools (emptyDir, populated by init containers)
		{
			Name:         VolumeTools,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		// Operator config (ConfigMap)
		{
			Name: VolumeConfig,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ObjectName(agent.Name, "config"),
					},
				},
			},
		},
	}

	// Data volume: PVC if explicitly configured, emptyDir otherwise (scratch space for tools)
	if !taskMode && agent.Spec.Storage != nil {
		volumes = append(volumes, corev1.Volume{
			Name: VolumeData,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ObjectName(agent.Name, "storage"),
				},
			},
		})
	} else {
		volumes = append(volumes, corev1.Volume{
			Name:         VolumeData,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}

	// Context files from ConfigMaps
	for i, cf := range agent.Spec.ContextFiles {
		volumes = append(volumes, corev1.Volume{
			Name: fmt.Sprintf("context-%d", i),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cf.ConfigMapRef.Name,
					},
					Items: []corev1.KeyToPath{
						{Key: cf.ConfigMapRef.Key, Path: cf.ConfigMapRef.Key},
					},
				},
			},
		})
	}

	// Pull secret volumes for OCI tools that need auth
	for _, tool := range agent.Spec.Tools {
		if tool.OCI.PullSecret != nil {
			volumes = append(volumes, corev1.Volume{
				Name: fmt.Sprintf("pull-secret-init-pull-tool-%s", tool.Name),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: tool.OCI.PullSecret.Name,
					},
				},
			})
		}
	}

	return volumes
}

func buildInitContainers(agent *agentsv1alpha1.Agent) []corev1.Container {
	inits := make([]corev1.Container, 0, len(agent.Spec.Tools))

	// OCI tool pulls
	for _, tool := range agent.Spec.Tools {
		inits = append(inits, buildCraneInitContainer(
			fmt.Sprintf("init-pull-tool-%s", tool.Name),
			tool.OCI.Ref,
			fmt.Sprintf("%s/%s", MountTools, tool.Name),
			VolumeTools,
			MountTools,
			tool.OCI.PullSecret,
		))
	}

	return inits
}

func buildCraneInitContainer(name, ref, destPath, volumeName, mountPath string, pullSecret *agentsv1alpha1.SecretKeyRef) corev1.Container {
	cmd := fmt.Sprintf("mkdir -p %s && crane export %s - | tar -xf - -C %s", ShellQuote(destPath), ShellQuote(ref), ShellQuote(destPath))

	c := corev1.Container{
		Name:    name,
		Image:   CraneImage,
		Command: []string{"sh", "-c", cmd},
		// Force HOME to a writable location so crane (and any sub-process)
		// can stat $HOME/.docker without hitting the read-only root fs.
		Env: []corev1.EnvVar{{Name: "HOME", Value: MountTmp}},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeName, MountPath: mountPath},
		},
	}

	// If a pull secret is specified, mount it as DOCKER_CONFIG directory.
	// crane expects DOCKER_CONFIG to point to a directory containing config.json.
	if pullSecret != nil {
		configDir := "/tmp/docker-config"
		// Write the dockerconfigjson to config.json and point crane at the dir
		cmd = fmt.Sprintf("mkdir -p %s && cp /tmp/pull-secret/%s %s/config.json && %s",
			configDir, pullSecret.Key, configDir, cmd)
		c.Command = []string{"sh", "-c", cmd}
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "DOCKER_CONFIG",
			Value: configDir,
		})
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("pull-secret-%s", name),
			MountPath: "/tmp/pull-secret",
			ReadOnly:  true,
		})
	}

	return c
}

func buildMainContainer(agent *agentsv1alpha1.Agent, providers []agentsv1alpha1.Provider, integrations []agentsv1alpha1.Integration, taskMode bool, infra InfraConfig) corev1.Container {
	volumeMounts := make([]corev1.VolumeMount, 0, 3+len(agent.Spec.ContextFiles))
	volumeMounts = append(volumeMounts,
		corev1.VolumeMount{Name: VolumeTools, MountPath: MountTools},
		corev1.VolumeMount{Name: VolumeConfig, MountPath: MountConfig},
	)

	// Data volume (always present — PVC if configured, emptyDir otherwise)
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name: VolumeData, MountPath: MountData,
	})

	// Context files
	for i, cf := range agent.Spec.ContextFiles {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("context-%d", i),
			MountPath: fmt.Sprintf("/etc/context/%s", cf.ConfigMapRef.Key),
			SubPath:   cf.ConfigMapRef.Key,
		})
	}

	// Environment variables
	env := buildEnvVars(agent, providers, integrations, taskMode, infra)

	// Build command: Fantasy runtime
	var command []string
	if taskMode {
		command = []string{"/app/agent-runtime", "task"}
	} else {
		command = []string{"/app/agent-runtime", "daemon"}
	}

	container := corev1.Container{
		Name:         ContainerRuntime,
		Image:        agent.RuntimeImage(),
		Command:      command,
		Env:          env,
		VolumeMounts: volumeMounts,
	}

	if agent.Spec.Resources != nil {
		container.Resources = *agent.Spec.Resources
	}
	ensureEphemeralStorage(&container.Resources)

	container.ImagePullPolicy = agent.RuntimeImagePullPolicy()

	// Health check and ports for daemon mode (contract: :4096)
	if !taskMode {
		container.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt32(AgentRuntimePort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
			TimeoutSeconds:      1,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt32(AgentRuntimePort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
			TimeoutSeconds:      1,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}
		container.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: AgentRuntimePort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	return container
}

// ====================================================================
// Environment variables
// ====================================================================

func buildEnvVars(agent *agentsv1alpha1.Agent, providers []agentsv1alpha1.Provider, integrations []agentsv1alpha1.Integration, taskMode bool, infra InfraConfig) []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0, 4+len(agent.Spec.Env)+len(agent.Spec.Secrets))

	// Agent metadata
	env = append(env,
		corev1.EnvVar{Name: "AGENT_NAME", Value: agent.Name},
		corev1.EnvVar{Name: "AGENT_NAMESPACE", Value: agent.Namespace},
		corev1.EnvVar{Name: "AGENT_MODE", Value: string(agent.Spec.Mode)},
		corev1.EnvVar{Name: "AGENT_RUNTIME", Value: "fantasy"},
		// WORKSPACE tells MCP tools (git, etc.) where the scratch/data directory is.
		// Always set to /data — the standard data volume mounted on every agent pod.
		corev1.EnvVar{Name: "WORKSPACE", Value: MountData},
	)

	// OpenTelemetry — always inject so the runtime exports traces to Tempo.
	env = append(env,
		corev1.EnvVar{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: infra.OTel()},
		corev1.EnvVar{Name: "OTEL_SERVICE_NAME", Value: agent.Name},
	)

	// NATS — always inject so the runtime can publish FEP events persistently.
	env = append(env,
		corev1.EnvVar{Name: "NATS_URL", Value: infra.NATS()},
	)

	// Plain-text env vars (sort for deterministic order)
	envKeys := make([]string, 0, len(agent.Spec.Env))
	for k := range agent.Spec.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		env = append(env, corev1.EnvVar{
			Name:  k,
			Value: agent.Spec.Env[k],
		})
	}

	// Tool-specific env vars (from inline tool bindings)
	for _, tool := range agent.Spec.Tools {
		for _, te := range tool.Env {
			env = append(env, corev1.EnvVar{
				Name:  te.Name,
				Value: te.Value,
			})
		}
	}

	// Secret-backed env vars
	for _, s := range agent.Spec.Secrets {
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

	// Provider API keys from Provider CRs (providerRefs).
	// Skipped for providers using oauth2ClientCredentials — auth is handled
	// inline by the runtime's OAuth2 transport.
	for _, prov := range providers {
		if prov.Spec.ApiKeySecret == nil && !providerNeedsOAuth2(prov) {
			continue
		}
		if prov.Spec.ApiKeySecret != nil {
			env = append(env, corev1.EnvVar{
				Name: ProviderEnvVarName(prov.Name),
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: prov.Spec.ApiKeySecret.Name},
						Key:                  prov.Spec.ApiKeySecret.Key,
					},
				},
			})
		}
		// OAuth2 client credentials: inject client_id and client_secret as env vars
		// on the main container (runtime reads them via the env var names in config.json).
		if providerNeedsOAuth2(prov) {
			oauth := prov.Spec.Endpoint.OAuth2ClientCredentials
			env = append(env,
				corev1.EnvVar{
					Name: OAuth2ClientIDEnvVar(prov.Name),
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: oauth.ClientIDSecret.Name},
							Key:                  oauth.ClientIDSecret.Key,
						},
					},
				},
				corev1.EnvVar{
					Name: OAuth2ClientSecretEnvVar(prov.Name),
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: oauth.ClientSecretSecret.Name},
							Key:                  oauth.ClientSecretSecret.Key,
						},
					},
				},
			)
		}
	}

	// Bound GitLab identity → native gitlab_* runtime tools (daemon only).
	// Task-mode runs get their GitLab identity from AgentRun.spec.git.
	if !taskMode && len(integrations) > 0 {
		bindings := make(map[string]agentsv1alpha1.IntegrationBinding, len(agent.Spec.Integrations))
		for _, b := range agent.Spec.Integrations {
			bindings[b.Name] = b
		}
		env = append(env, GitLabEnvFromIntegrations(integrations, bindings)...)
	}

	return env
}

// ====================================================================
// OAuth2 helpers (replaces token-injector sidecar since v0.16.0)
// ====================================================================

// providerNeedsOAuth2 reports whether a Provider has OAuth2
// client_credentials configured.
func providerNeedsOAuth2(prov agentsv1alpha1.Provider) bool {
	return prov.Spec.Endpoint != nil &&
		prov.Spec.Endpoint.OAuth2ClientCredentials != nil
}
