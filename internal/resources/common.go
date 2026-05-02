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
	"os"
	"regexp"
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

	// DefaultMCPGatewayImage is the default MCP protocol gateway image
	// (spawn + proxy modes). Overridable at runtime via the
	// AGENTOPS_MCP_GATEWAY_IMAGE env var on the manager Deployment.
	DefaultMCPGatewayImage = "ghcr.io/samyn92/mcp-gateway:latest"

	// DefaultTokenInjectorImage is the default OAuth2 client_credentials
	// token-injector sidecar image. Overridable at runtime via the
	// AGENTOPS_TOKEN_INJECTOR_IMAGE env var on the manager Deployment.
	DefaultTokenInjectorImage = "ghcr.io/samyn92/token-injector:latest"

	// envMCPGatewayImage is the env var consulted by MCPGatewayImage().
	envMCPGatewayImage = "AGENTOPS_MCP_GATEWAY_IMAGE"

	// envTokenInjectorImage is the env var consulted by TokenInjectorImage().
	envTokenInjectorImage = "AGENTOPS_TOKEN_INJECTOR_IMAGE"

	// TokenInjectorBasePort is the starting port for token-injector sidecars
	// (one per Provider with OAuth2ClientCredentials enabled).
	TokenInjectorBasePort = 9101

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

	// DefaultOTelEndpoint is the in-cluster Tempo OTLP gRPC endpoint.
	// Injected unconditionally into agent pods so the runtime can export traces.
	DefaultOTelEndpoint = "tempo.observability.svc.cluster.local:4317"

	// DefaultNATSEndpoint is the in-cluster NATS URL for FEP event publishing.
	// Injected unconditionally into agent pods so the runtime can publish FEP
	// events via NATS even when no browser SSE stream is active.
	DefaultNATSEndpoint = "nats://agentops-nats.agent-system.svc.cluster.local:4222"
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

// ociRefPattern matches valid OCI image references:
// [registry/]repository[:tag|@digest]
// Only allows alphanumeric, dots, dashes, underscores, colons, slashes, and @sha256: digests.
var ociRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-/:@]+$`)

// ValidateOCIRef checks that an OCI reference contains no shell metacharacters.
// Returns an error if the ref could be used for shell injection.
func ValidateOCIRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("OCI reference is empty")
	}
	if !ociRefPattern.MatchString(ref) {
		return fmt.Errorf("OCI reference %q contains invalid characters", ref)
	}
	return nil
}

// ShellQuote wraps a string in single quotes with proper escaping for sh -c.
// This is defense-in-depth for values interpolated into shell commands.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// MCPGatewayImage returns the MCP gateway image reference, honoring the
// AGENTOPS_MCP_GATEWAY_IMAGE env var override and falling back to
// DefaultMCPGatewayImage. Read on every call so operator config changes
// take effect on the next reconcile without a process restart in tests.
func MCPGatewayImage() string {
	if v := os.Getenv(envMCPGatewayImage); v != "" {
		return v
	}
	return DefaultMCPGatewayImage
}

// TokenInjectorImage returns the OAuth2 token-injector sidecar image
// reference, honoring the AGENTOPS_TOKEN_INJECTOR_IMAGE env var override
// and falling back to DefaultTokenInjectorImage.
func TokenInjectorImage() string {
	if v := os.Getenv(envTokenInjectorImage); v != "" {
		return v
	}
	return DefaultTokenInjectorImage
}
