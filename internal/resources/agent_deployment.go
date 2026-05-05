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
func BuildAgentDeployment(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, providers []agentsv1alpha1.Provider, infra InfraConfig) *appsv1.Deployment {
	labels := CommonLabels(agent.Name, "runtime")
	var replicas int32 = 1

	// Build pod spec
	podSpec := buildAgentPodSpec(agent, agentTools, providers, false, infra)

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

// agentToolByName returns the AgentTool with the given name, or nil if not found.
func agentToolByName(name string, agentTools []agentsv1alpha1.AgentTool) *agentsv1alpha1.AgentTool {
	for i := range agentTools {
		if agentTools[i].Name == name {
			return &agentTools[i]
		}
	}
	return nil
}

// hasMCPTools returns true if any of the agent's tool bindings reference MCP-source AgentTools.
func hasMCPTools(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool) bool {
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool != nil && tool.IsMCPSource() {
			return true
		}
	}
	return false
}

// buildAgentPodSpec creates the complete PodSpec for daemon or task mode.
// taskMode=true uses emptyDir for /data instead of PVC.
func buildAgentPodSpec(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, providers []agentsv1alpha1.Provider, taskMode bool, infra InfraConfig) corev1.PodSpec {
	// Volumes
	volumes := buildVolumes(agent, agentTools, taskMode)

	// Init containers: OCI pulls
	initContainers := buildInitContainers(agent, agentTools)

	// Main container
	mainContainer := buildMainContainer(agent, agentTools, providers, taskMode, infra)

	// Sidecar containers: MCP gateway proxies for MCP-source tools
	var sidecars []corev1.Container
	mcpIndex := 0
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool == nil || !tool.IsMCPSource() {
			continue
		}
		sidecar := buildGatewaySidecar(binding, tool, mcpIndex)
		if sidecar != nil {
			sidecars = append(sidecars, *sidecar)
		}
		mcpIndex++
	}

	// Sidecar containers: OAuth2 token-injector proxies for Providers
	// using the client_credentials grant (one sidecar per such Provider).
	for i := range providers {
		prov := &providers[i]
		if !providerNeedsTokenInjector(prov) {
			continue
		}
		sidecar := buildTokenInjectorSidecar(prov, i)
		if sidecar != nil {
			sidecars = append(sidecars, *sidecar)
		}
	}

	containers := append([]corev1.Container{mainContainer}, sidecars...)

	podSpec := corev1.PodSpec{
		InitContainers:     initContainers,
		Containers:         containers,
		Volumes:            volumes,
		ServiceAccountName: AgentServiceAccountName(agent),
	}

	// Apply restricted-by-default security on every container/init/sidecar
	// and the pod itself, merged with any user-supplied overrides. The
	// controller calls ComputeSecurityViolations(agent.Spec.Security)
	// separately to surface clamps on .status.conditions.
	ApplySecurity(&podSpec, ContainerRuntime, agent.Spec.Security)

	return podSpec
}

func buildVolumes(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, taskMode bool) []corev1.Volume {
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

	// Gateway + MCP config volumes (if MCP-source tools present)
	if hasMCPTools(agent, agentTools) {
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

	// ConfigMap-based tools mounted as volumes
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool != nil && tool.Spec.ConfigMap != nil {
			volumes = append(volumes, corev1.Volume{
				Name: fmt.Sprintf("tool-cm-%s", binding.Name),
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: tool.Spec.ConfigMap.Name,
						},
						Items: []corev1.KeyToPath{
							{Key: tool.Spec.ConfigMap.Key, Path: "index.js"},
						},
					},
				},
			})
		}
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
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool != nil && tool.Spec.OCI != nil && tool.Spec.OCI.PullSecret != nil {
			volumes = append(volumes, corev1.Volume{
				Name: fmt.Sprintf("pull-secret-init-pull-tool-%s", binding.Name),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: tool.Spec.OCI.PullSecret.Name,
					},
				},
			})
		}
		if tool != nil && tool.Spec.Skill != nil && tool.Spec.Skill.PullSecret != nil {
			volumes = append(volumes, corev1.Volume{
				Name: fmt.Sprintf("pull-secret-init-pull-skill-%s", binding.Name),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: tool.Spec.Skill.PullSecret.Name,
					},
				},
			})
		}
	}

	return volumes
}

func buildInitContainers(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool) []corev1.Container {
	var inits []corev1.Container

	// OCI tool pulls and skill pulls
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool == nil {
			continue
		}
		if tool.Spec.OCI != nil {
			inits = append(inits, buildCraneInitContainer(
				fmt.Sprintf("init-pull-tool-%s", binding.Name),
				tool.Spec.OCI.Ref,
				fmt.Sprintf("%s/%s", MountTools, binding.Name),
				VolumeTools,
				MountTools,
				tool.Spec.OCI.PullSecret,
			))
		}
		if tool.Spec.Skill != nil {
			inits = append(inits, buildCraneInitContainer(
				fmt.Sprintf("init-pull-skill-%s", binding.Name),
				tool.Spec.Skill.Ref,
				fmt.Sprintf("%s/%s", MountTools, binding.Name),
				VolumeTools,
				MountTools,
				tool.Spec.Skill.PullSecret,
			))
		}
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

func buildMainContainer(agent *agentsv1alpha1.Agent, agentTools []agentsv1alpha1.AgentTool, providers []agentsv1alpha1.Provider, taskMode bool, infra InfraConfig) corev1.Container {
	volumeMounts := []corev1.VolumeMount{
		{Name: VolumeTools, MountPath: MountTools},
		{Name: VolumeConfig, MountPath: MountConfig},
	}

	// Data volume (always present — PVC if configured, emptyDir otherwise)
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name: VolumeData, MountPath: MountData,
	})

	// MCP config mount (if any MCP-source tools)
	if hasMCPTools(agent, agentTools) {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      VolumeMCP,
			MountPath: MountMCP,
		})
	}

	// ConfigMap-based tools
	for _, binding := range agent.Spec.Tools {
		tool := agentToolByName(binding.Name, agentTools)
		if tool != nil && tool.Spec.ConfigMap != nil {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      fmt.Sprintf("tool-cm-%s", binding.Name),
				MountPath: fmt.Sprintf("%s/%s", MountTools, binding.Name),
			})
		}
	}

	// Context files
	for i, cf := range agent.Spec.ContextFiles {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("context-%d", i),
			MountPath: fmt.Sprintf("/etc/context/%s", cf.ConfigMapRef.Key),
			SubPath:   cf.ConfigMapRef.Key,
		})
	}

	// Environment variables
	env := buildEnvVars(agent, providers, infra)

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

func buildEnvVars(agent *agentsv1alpha1.Agent, providers []agentsv1alpha1.Provider, infra InfraConfig) []corev1.EnvVar {
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
	// by the per-provider token-injector sidecar.
	for _, prov := range providers {
		if prov.Spec.ApiKeySecret == nil {
			continue
		}
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

	return env
}

// ====================================================================
// MCP Gateway sidecar
// ====================================================================

func buildGatewaySidecar(binding agentsv1alpha1.AgentToolBinding, tool *agentsv1alpha1.AgentTool, index int) *corev1.Container {
	// Determine the upstream URL from the AgentTool's status
	upstream := tool.Status.ServiceURL
	if upstream == "" {
		// Fallback: compute from the tool spec
		upstream = AgentToolServiceURL(tool)
	}

	if upstream == "" {
		return nil
	}

	port := int32(GatewayBasePort + index)

	return &corev1.Container{
		Name:  fmt.Sprintf("gw-%s", binding.Name),
		Image: MCPGatewayImage(),
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
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       30,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
			},
			InitialDelaySeconds: 2,
			PeriodSeconds:       10,
		},
		Resources: SidecarResources(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolumeGateway, MountPath: MountGateway, ReadOnly: true},
		},
	}
}

// ====================================================================
// OAuth2 Token-Injector sidecar (per Provider)
// ====================================================================

// providerNeedsTokenInjector reports whether a Provider has OAuth2
// client_credentials configured and therefore requires a sidecar.
func providerNeedsTokenInjector(prov *agentsv1alpha1.Provider) bool {
	return prov != nil &&
		prov.Spec.Endpoint != nil &&
		prov.Spec.Endpoint.OAuth2ClientCredentials != nil
}

// TokenInjectorPort returns the localhost port the token-injector for the
// given provider listens on. The agent talks to http://localhost:<port>.
func TokenInjectorPort(index int) int32 {
	return int32(TokenInjectorBasePort + index)
}

// buildTokenInjectorSidecar builds the OAuth2 client_credentials token-injector
// sidecar for a single Provider. It exposes a localhost endpoint that the agent
// container points at via the Provider's BaseURL override in config.json.
func buildTokenInjectorSidecar(prov *agentsv1alpha1.Provider, index int) *corev1.Container {
	if !providerNeedsTokenInjector(prov) {
		return nil
	}
	oauth := prov.Spec.Endpoint.OAuth2ClientCredentials
	target := prov.Spec.Endpoint.BaseURL
	if target == "" {
		// Without a target URL there is nothing to forward to.
		return nil
	}
	port := TokenInjectorPort(index)

	env := []corev1.EnvVar{
		{Name: "TOKEN_INJECTOR_TARGET_URL", Value: target},
		{Name: "TOKEN_INJECTOR_TOKEN_URL", Value: oauth.TokenURL},
		{Name: "TOKEN_INJECTOR_LISTEN_PORT", Value: fmt.Sprintf("%d", port)},
		{
			Name: "TOKEN_INJECTOR_CLIENT_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: oauth.ClientIDSecret.Name},
					Key:                  oauth.ClientIDSecret.Key,
				},
			},
		},
		{
			Name: "TOKEN_INJECTOR_CLIENT_SECRET",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: oauth.ClientSecretSecret.Name},
					Key:                  oauth.ClientSecretSecret.Key,
				},
			},
		},
	}
	if oauth.Scope != "" {
		env = append(env, corev1.EnvVar{Name: "TOKEN_INJECTOR_SCOPE", Value: oauth.Scope})
	}
	if oauth.Audience != "" {
		env = append(env, corev1.EnvVar{Name: "TOKEN_INJECTOR_AUDIENCE", Value: oauth.Audience})
	}

	return &corev1.Container{
		Name:  fmt.Sprintf("ti-%s", prov.Name),
		Image: TokenInjectorImage(),
		Env:   env,
		Ports: []corev1.ContainerPort{
			{
				Name:          fmt.Sprintf("ti-%d", index),
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       30,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
			},
			InitialDelaySeconds: 2,
			PeriodSeconds:       10,
		},
		Resources: SidecarResources(),
	}
}
