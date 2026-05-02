{{/*
Expand the name of the chart.
*/}}
{{- define "agentops-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "agentops-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "agentops-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "agentops-operator.labels" -}}
helm.sh/chart: {{ include "agentops-operator.chart" . }}
{{ include "agentops-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Selector labels
*/}}
{{- define "agentops-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agentops-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "agentops-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "agentops-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the leader election role
*/}}
{{- define "agentops-operator.leaderElectionRoleName" -}}
{{- printf "%s-leader-election" (include "agentops-operator.fullname" .) }}
{{- end }}

{{/*
Create the leader election ID
*/}}
{{- define "agentops-operator.leaderElectionId" -}}
{{- .Values.leaderElection.id | default "e58828d7.agentops.io" }}
{{- end }}

{{/*
Operator image
*/}}
{{- define "agentops-operator.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}
{{- end }}

{{/*
MCP gateway sidecar image (used by the operator at agent-pod build time).
*/}}
{{- define "agentops-operator.mcpGatewayImage" -}}
{{- printf "%s:%s" .Values.sidecarImages.mcpGateway.repository (.Values.sidecarImages.mcpGateway.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
OAuth2 token-injector sidecar image.
*/}}
{{- define "agentops-operator.tokenInjectorImage" -}}
{{- printf "%s:%s" .Values.sidecarImages.tokenInjector.repository (.Values.sidecarImages.tokenInjector.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
Manager RBAC rules — shared between ClusterRole and namespaced Roles.
*/}}
{{- define "agentops-operator.managerRules" -}}
# Core resources
- apiGroups:
    - ""
  resources:
    - configmaps
    - persistentvolumeclaims
    - secrets
    - serviceaccounts
    - services
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
- apiGroups:
    - ""
  resources:
    - pods
  verbs:
    - get
    - list
    - watch
- apiGroups:
    - ""
  resources:
    - pods/log
  verbs:
    - get
# AgentOps CRDs
- apiGroups:
    - agents.agentops.io
  resources:
    - agentresources
    - agentruns
    - agents
    - agenttools
    - channels
    - providers
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
- apiGroups:
    - agents.agentops.io
  resources:
    - agentresources/finalizers
    - agentruns/finalizers
    - agents/finalizers
    - agenttools/finalizers
    - channels/finalizers
    - providers/finalizers
  verbs:
    - update
- apiGroups:
    - agents.agentops.io
  resources:
    - agentresources/status
    - agentruns/status
    - agents/status
    - agenttools/status
    - channels/status
    - providers/status
  verbs:
    - get
    - patch
    - update
# Deployments
- apiGroups:
    - apps
  resources:
    - deployments
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
# Jobs
- apiGroups:
    - batch
  resources:
    - jobs
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
# Networking
- apiGroups:
    - networking.k8s.io
  resources:
    - ingresses
    - networkpolicies
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
# RBAC (agent pod permissions)
- apiGroups:
    - rbac.authorization.k8s.io
  resources:
    - roles
    - rolebindings
  verbs:
    - create
    - delete
    - get
    - list
    - patch
    - update
    - watch
{{- end }}
