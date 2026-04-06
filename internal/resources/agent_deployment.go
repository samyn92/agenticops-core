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
	"strings"

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildAgentDeployment creates the Deployment for a daemon agent.
func BuildAgentDeployment(agent *agentsv1alpha1.Agent, mcpServers []agentsv1alpha1.MCPServer) *appsv1.Deployment {
	labels := CommonLabels(agent.Name, "runtime")
	var replicas int32 = 1

	// Build pod spec
	podSpec := buildAgentPodSpec(agent, mcpServers, false)

	return &appsv1.Deployment{
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
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}

// buildAgentPodSpec creates the complete PodSpec for daemon or task mode.
// taskMode=true uses emptyDir for /data instead of PVC.
func buildAgentPodSpec(agent *agentsv1alpha1.Agent, mcpServers []agentsv1alpha1.MCPServer, taskMode bool) corev1.PodSpec {
	// Volumes
	volumes := buildVolumes(agent, taskMode)

	// Init containers: OCI pulls
	initContainers := buildInitContainers(agent)

	// Main container
	mainContainer := buildMainContainer(agent, taskMode)

	// Sidecar containers: MCP gateway proxies
	var sidecars []corev1.Container
	for i, ms := range agent.Spec.MCPServers {
		sidecar := buildGatewaySidecar(ms, mcpServers, i)
		if sidecar != nil {
			sidecars = append(sidecars, *sidecar)
		}
	}

	containers := append([]corev1.Container{mainContainer}, sidecars...)

	podSpec := corev1.PodSpec{
		InitContainers: initContainers,
		Containers:     containers,
		Volumes:        volumes,
	}

	if agent.Spec.ServiceAccountName != "" {
		podSpec.ServiceAccountName = agent.Spec.ServiceAccountName
	}

	return podSpec
}

func buildVolumes(agent *agentsv1alpha1.Agent, taskMode bool) []corev1.Volume {
	volumes := []corev1.Volume{
		// Tools (emptyDir, populated by init containers — shared across runtimes)
		{
			Name:         VolumeTools,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		// Operator config (ConfigMap — shared across runtimes)
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

	// Pi-specific volumes: extensions, skills
	if agent.Spec.Pi != nil {
		volumes = append(volumes,
			corev1.Volume{
				Name:         VolumeExtensions,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
			corev1.Volume{
				Name:         VolumeSkills,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		)
	}

	// Data volume: PVC for daemon, emptyDir for task
	if taskMode {
		volumes = append(volumes, corev1.Volume{
			Name:         VolumeData,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	} else if agent.Spec.Storage != nil {
		volumes = append(volumes, corev1.Volume{
			Name: VolumeData,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ObjectName(agent.Name, "storage"),
				},
			},
		})
	}

	// Gateway config (if MCP servers)
	if len(agent.Spec.MCPServers) > 0 {
		volumes = append(volumes,
			corev1.Volume{
				Name: VolumeGateway,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ObjectName(agent.Name, "gateway"),
						},
					},
				},
			},
			corev1.Volume{
				Name: VolumeMCP,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ObjectName(agent.Name, "mcp"),
						},
					},
				},
			},
		)
	}

	// ConfigMap-based tools mounted as volumes (shared across runtimes)
	for _, tr := range agent.Spec.ToolRefs {
		if tr.ConfigMapRef != nil {
			volumes = append(volumes, corev1.Volume{
				Name: fmt.Sprintf("tool-cm-%s", tr.Name),
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: tr.ConfigMapRef.Name,
						},
						Items: []corev1.KeyToPath{
							{Key: tr.ConfigMapRef.Key, Path: "index.js"},
						},
					},
				},
			})
		}
	}

	// Pi-specific: ConfigMap-based skills mounted as volumes
	if agent.Spec.Pi != nil {
		for _, sk := range agent.Spec.Pi.Skills {
			if sk.ConfigMapRef != nil {
				volumes = append(volumes, corev1.Volume{
					Name: fmt.Sprintf("skill-cm-%s", sk.Name),
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: sk.ConfigMapRef.Name,
							},
							Items: []corev1.KeyToPath{
								{Key: sk.ConfigMapRef.Key, Path: "SKILL.md"},
							},
						},
					},
				})
			}
		}
	}

	// Context files from ConfigMaps (shared across runtimes)
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

	return volumes
}

func buildInitContainers(agent *agentsv1alpha1.Agent) []corev1.Container {
	var inits []corev1.Container

	// OCI tool pulls (shared across runtimes)
	for _, tr := range agent.Spec.ToolRefs {
		if tr.OCIRef != nil {
			inits = append(inits, buildCraneInitContainer(
				fmt.Sprintf("init-pull-tool-%s", tr.Name),
				tr.OCIRef.Ref,
				fmt.Sprintf("%s/%s", MountTools, tr.Name),
				VolumeTools,
				MountTools,
				tr.OCIRef.PullSecret,
			))
		}
	}

	// Pi-specific: extension pulls
	if agent.Spec.Pi != nil {
		for _, ext := range agent.Spec.Pi.Extensions {
			inits = append(inits, buildCraneInitContainer(
				fmt.Sprintf("init-pull-ext-%s", ext.Name),
				ext.OCIRef.Ref,
				fmt.Sprintf("%s/%s", MountExtensions, ext.Name),
				VolumeExtensions,
				MountExtensions,
				ext.OCIRef.PullSecret,
			))
		}

		// Pi-specific: skill pulls
		for _, sk := range agent.Spec.Pi.Skills {
			if sk.OCIRef != nil {
				inits = append(inits, buildCraneInitContainer(
					fmt.Sprintf("init-pull-skill-%s", sk.Name),
					sk.OCIRef.Ref,
					fmt.Sprintf("%s/%s", MountSkills, sk.Name),
					VolumeSkills,
					MountSkills,
					sk.OCIRef.PullSecret,
				))
			}
		}
	}

	return inits
}

func buildCraneInitContainer(name, ref, destPath, volumeName, mountPath string, pullSecret *agentsv1alpha1.SecretKeyRef) corev1.Container {
	cmd := fmt.Sprintf("mkdir -p %s && crane export %s - | tar -xf - -C %s", destPath, ref, destPath)

	c := corev1.Container{
		Name:    name,
		Image:   CraneImage,
		Command: []string{"sh", "-c", cmd},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeName, MountPath: mountPath},
		},
	}

	// If a pull secret is specified, mount it for crane auth
	if pullSecret != nil {
		c.Env = append(c.Env, corev1.EnvVar{
			Name: "DOCKER_CONFIG",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: pullSecret.Name},
					Key:                  pullSecret.Key,
				},
			},
		})
	}

	return c
}

func buildMainContainer(agent *agentsv1alpha1.Agent, taskMode bool) corev1.Container {
	volumeMounts := []corev1.VolumeMount{
		{Name: VolumeTools, MountPath: MountTools},
		{Name: VolumeConfig, MountPath: MountConfig},
	}

	// Data volume (only if storage is configured or task mode)
	if taskMode || agent.Spec.Storage != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: VolumeData, MountPath: MountData,
		})
	}

	// Pi-specific mounts: extensions, skills
	if agent.Spec.Pi != nil {
		volumeMounts = append(volumeMounts,
			corev1.VolumeMount{Name: VolumeExtensions, MountPath: MountExtensions},
			corev1.VolumeMount{Name: VolumeSkills, MountPath: MountSkills},
		)
	}

	// MCP config mount (shared)
	if len(agent.Spec.MCPServers) > 0 {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      VolumeMCP,
			MountPath: MountMCP,
		})
	}

	// ConfigMap-based tools (shared)
	for _, tr := range agent.Spec.ToolRefs {
		if tr.ConfigMapRef != nil {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      fmt.Sprintf("tool-cm-%s", tr.Name),
				MountPath: fmt.Sprintf("%s/%s", MountTools, tr.Name),
			})
		}
	}

	// Pi-specific: ConfigMap-based skills
	if agent.Spec.Pi != nil {
		for _, sk := range agent.Spec.Pi.Skills {
			if sk.ConfigMapRef != nil {
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name:      fmt.Sprintf("skill-cm-%s", sk.Name),
					MountPath: fmt.Sprintf("%s/%s", MountSkills, sk.Name),
				})
			}
		}
	}

	// Context files (shared)
	for i, cf := range agent.Spec.ContextFiles {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("context-%d", i),
			MountPath: fmt.Sprintf("/etc/context/%s", cf.ConfigMapRef.Key),
			SubPath:   cf.ConfigMapRef.Key,
		})
	}

	// Environment variables (shared)
	env := buildEnvVars(agent)

	// Build command and args based on runtime
	var command []string
	var args []string

	switch {
	case agent.Spec.Pi != nil:
		command, args = buildPiCommand(agent, taskMode)
	case agent.Spec.Fantasy != nil:
		command, args = buildFantasyCommand(taskMode)
	}

	container := corev1.Container{
		Name:         "agent-runtime",
		Image:        agent.RuntimeImage(),
		Command:      command,
		Args:         args,
		Env:          env,
		VolumeMounts: volumeMounts,
	}

	if agent.Spec.Resources != nil {
		container.Resources = *agent.Spec.Resources
	}

	container.ImagePullPolicy = agent.RuntimeImagePullPolicy()

	// Health check and ports for daemon mode (shared contract: :4096)
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
// Runtime-specific command builders
// ====================================================================

func buildPiCommand(agent *agentsv1alpha1.Agent, taskMode bool) ([]string, []string) {
	var command []string
	if taskMode {
		command = []string{"node", "/app/agent-task.js"}
	} else {
		command = []string{"node", "/app/agent-server.js"}
	}

	var args []string
	if len(agent.Spec.Pi.BuiltinTools) > 0 {
		args = append(args, "--tools", strings.Join(agent.Spec.Pi.BuiltinTools, ","))
	} else {
		args = append(args, "--no-tools")
	}

	return command, args
}

func buildFantasyCommand(taskMode bool) ([]string, []string) {
	if taskMode {
		return []string{"/app/agent-runtime", "task"}, []string{}
	}
	return []string{"/app/agent-runtime", "daemon"}, []string{}
}

// ====================================================================
// Environment variables (shared across runtimes)
// ====================================================================

func buildEnvVars(agent *agentsv1alpha1.Agent) []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0, 4+len(agent.Spec.Env)+len(agent.Spec.Secrets))

	// Agent metadata
	env = append(env,
		corev1.EnvVar{Name: "AGENT_NAME", Value: agent.Name},
		corev1.EnvVar{Name: "AGENT_NAMESPACE", Value: agent.Namespace},
		corev1.EnvVar{Name: "AGENT_MODE", Value: string(agent.Spec.Mode)},
		corev1.EnvVar{Name: "AGENT_RUNTIME", Value: agent.RuntimeType()},
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

	// Provider API keys as env vars (shared)
	for _, p := range agent.Spec.Providers {
		env = append(env, corev1.EnvVar{
			Name: ProviderEnvVarName(p.Name),
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: p.ApiKeySecret.Name},
					Key:                  p.ApiKeySecret.Key,
				},
			},
		})
	}

	// Pi-specific: extension env vars
	if agent.Spec.Pi != nil {
		for _, ext := range agent.Spec.Pi.Extensions {
			extEnvKeys := make([]string, 0, len(ext.Env))
			for k := range ext.Env {
				extEnvKeys = append(extEnvKeys, k)
			}
			sort.Strings(extEnvKeys)
			for _, k := range extEnvKeys {
				env = append(env, corev1.EnvVar{
					Name:  k,
					Value: ext.Env[k],
				})
			}
			for _, s := range ext.Secrets {
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
		}
	}

	return env
}

// ====================================================================
// MCP Gateway sidecar (shared across runtimes)
// ====================================================================

func buildGatewaySidecar(binding agentsv1alpha1.MCPServerBinding, mcpServers []agentsv1alpha1.MCPServer, index int) *corev1.Container {
	// Find the MCPServer to get its service URL
	var upstream string
	for _, mcp := range mcpServers {
		if mcp.Name == binding.Name {
			if mcp.Spec.URL != "" {
				// External mode
				upstream = mcp.Spec.URL
			} else {
				// Deploy mode
				upstream = MCPServerServiceURL(&mcp)
			}
			break
		}
	}

	if upstream == "" {
		return nil
	}

	port := int32(GatewayBasePort + index)

	return &corev1.Container{
		Name:  fmt.Sprintf("gw-%s", binding.Name),
		Image: "ghcr.io/samyn92/mcp-gateway:latest",
		Env: []corev1.EnvVar{
			{Name: "GATEWAY_MODE", Value: "proxy"},
			{Name: "GATEWAY_UPSTREAM", Value: upstream},
			{Name: "GATEWAY_PORT", Value: fmt.Sprintf("%d", port)},
			{Name: "GATEWAY_CONFIG", Value: fmt.Sprintf("%s/permissions.json", MountGateway)},
			{Name: "GATEWAY_SERVER_NAME", Value: binding.Name},
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          fmt.Sprintf("gw-%d", index),
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolumeGateway, MountPath: MountGateway, ReadOnly: true},
		},
	}
}
