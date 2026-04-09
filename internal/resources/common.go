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

// Package resources contains builders that translate CRD specs into
// concrete Kubernetes resources (Deployments, Jobs, ConfigMaps, etc.).
package resources

import (
	"fmt"
	"strings"
)

const (
	// LabelAgent is the standard label for the owning agent name.
	LabelAgent = "agents.agentops.io/agent"
	// LabelComponent distinguishes operator-created components.
	LabelComponent = "agents.agentops.io/component"
	// LabelManagedBy marks resources managed by the operator.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value for the managed-by label.
	ManagedByValue = "agentops-operator"

	// AgentRuntimePort is the HTTP port for the agent runtime.
	AgentRuntimePort = 4096
	// MCPServerDefaultPort is the default port for MCP servers.
	MCPServerDefaultPort = 8080
	// GatewayBasePort is the starting port for MCP gateway proxy sidecars.
	GatewayBasePort = 9001

	// CraneImage is the OCI puller used in init containers.
	CraneImage = "gcr.io/go-containerregistry/crane:debug"

	// MCPGatewayImage is the MCP protocol gateway image (spawn + proxy modes).
	MCPGatewayImage = "ghcr.io/samyn92/mcp-gateway:latest"

	// Volume names.
	VolumeData    = "data"
	VolumeTools   = "tools"
	VolumeConfig  = "operator-config"
	VolumeGateway = "gateway-config"
	VolumeMCP     = "mcp-config"

	// Container names.
	ContainerRuntime = "agent-runtime"

	// Mount paths.
	MountData    = "/data"
	MountTools   = "/tools"
	MountConfig  = "/etc/operator"
	MountGateway = "/etc/gateway"
	MountMCP     = "/etc/mcp"
)

// CommonLabels returns the standard set of labels for an agent-owned resource.
func CommonLabels(agentName, component string) map[string]string {
	return map[string]string{
		LabelAgent:     agentName,
		LabelComponent: component,
		LabelManagedBy: ManagedByValue,
	}
}

// ObjectName returns the conventional name for a sub-resource.
func ObjectName(agentName, suffix string) string {
	if suffix == "" {
		return agentName
	}
	return fmt.Sprintf("%s-%s", agentName, suffix)
}

// MCPServerObjectName returns the conventional name for MCPServer sub-resources.
func MCPServerObjectName(mcpName string) string {
	return fmt.Sprintf("mcp-%s", mcpName)
}

// joinCommand joins command parts into a single space-separated string.
func joinCommand(parts []string) string {
	return strings.Join(parts, " ")
}
