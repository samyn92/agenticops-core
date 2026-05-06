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

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	"github.com/samyn92/agentops-core/internal/resources"
	"github.com/samyn92/agentops-core/internal/tracing"
)

// AgentRunReconciler reconciles an AgentRun object.
type AgentRunReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
	Infra      resources.InfraConfig
}

// +kubebuilder:rbac:groups=agents.agentops.io,resources=agentruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agentruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.agentops.io,resources=agenttools,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;pods/log,verbs=get;list;watch

func (r *AgentRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, span := tracing.Tracer.Start(ctx, "operator.reconcile.agentrun",
		trace.WithAttributes(
			attribute.String("k8s.name", req.Name),
			attribute.String("k8s.namespace", req.Namespace),
		),
	)
	defer span.End()

	log := logf.FromContext(ctx)

	// Fetch AgentRun
	run := &agentsv1alpha1.AgentRun{}
	if err := r.Get(ctx, req.NamespacedName, run); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "fetch agentrun")
		return ctrl.Result{}, err
	}

	span.SetAttributes(attribute.String("agentrun.agent_ref", run.Spec.AgentRef))

	// Skip terminal states
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		run.Status.Phase == agentsv1alpha1.AgentRunPhaseFailed {
		span.SetAttributes(attribute.String("agentrun.phase", string(run.Status.Phase)))
		return ctrl.Result{}, nil
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(run.DeepCopy())

	log.Info("Reconciling AgentRun", "name", run.Name, "phase", run.Status.Phase)

	// Resolve the target Agent
	_, resolveSpan := tracing.Tracer.Start(ctx, "operator.resolve.agent",
		trace.WithAttributes(attribute.String("agent.name", run.Spec.AgentRef)),
	)
	agent := &agentsv1alpha1.Agent{}
	if err := r.Get(ctx, types.NamespacedName{Name: run.Spec.AgentRef, Namespace: run.Namespace}, agent); err != nil {
		resolveSpan.RecordError(err)
		resolveSpan.SetStatus(codes.Error, "agent not found")
		resolveSpan.End()
		r.setRunFailedStatus(run, fmt.Sprintf("Agent %q not found", run.Spec.AgentRef))
		if patchErr := patchStatus(ctx, r.Client, run, statusPatch); patchErr != nil {
			span.RecordError(patchErr)
			span.SetStatus(codes.Error, "patch status")
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}
	resolveSpan.SetAttributes(attribute.String("agent.mode", string(agent.Spec.Mode)))
	resolveSpan.End()

	span.SetAttributes(attribute.String("agent.mode", string(agent.Spec.Mode)))

	// Ensure the agent label is set on the AgentRun CR for concurrency tracking.
	// Without this label, checkConcurrency cannot find active runs for this agent.
	if run.Labels == nil || run.Labels[resources.LabelAgent] != agent.Name {
		if run.Labels == nil {
			run.Labels = make(map[string]string)
		}
		run.Labels[resources.LabelAgent] = agent.Name
		if err := r.Update(ctx, run); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set the mode from the agent
	run.Status.Mode = agent.Spec.Mode

	// For daemon mode, check that the Agent is Running before attempting to send a prompt.
	// This avoids noisy HTTP errors when the agent pod isn't up yet (e.g. waiting on MCPServer deps).
	if agent.Spec.Mode == agentsv1alpha1.AgentModeDaemon &&
		run.Status.Phase != agentsv1alpha1.AgentRunPhaseRunning {
		if agent.Status.Phase != agentsv1alpha1.AgentPhaseRunning {
			if run.Status.Phase != agentsv1alpha1.AgentRunPhasePending {
				run.Status.Phase = agentsv1alpha1.AgentRunPhasePending
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:    "AgentReady",
					Status:  metav1.ConditionFalse,
					Reason:  "AgentNotRunning",
					Message: fmt.Sprintf("Agent %q is in phase %q, waiting for Running", agent.Name, agent.Status.Phase),
				})
				if err := patchStatus(ctx, r.Client, run, statusPatch); err != nil {
					return ctrl.Result{}, err
				}
			}
			log.Info("Agent not running, requeuing", "agent", agent.Name, "agentPhase", agent.Status.Phase)
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
	}

	// Check concurrency
	allowed, err := r.checkConcurrency(ctx, run, agent, statusPatch)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !allowed {
		// Already set to Queued or Failed and patched
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Set initial phase so the run is always visible as Pending at minimum
	if run.Status.Phase == "" {
		run.Status.Phase = agentsv1alpha1.AgentRunPhasePending
	}

	// Execute based on agent mode
	var result ctrl.Result
	var reconcileErr error

	_, modeSpan := tracing.Tracer.Start(ctx, "operator.dispatch."+string(agent.Spec.Mode),
		trace.WithAttributes(
			attribute.String("agentrun.name", run.Name),
			attribute.String("agent.mode", string(agent.Spec.Mode)),
		),
	)

	switch agent.Spec.Mode {
	case agentsv1alpha1.AgentModeTask:
		result, reconcileErr = r.reconcileTaskRun(ctx, run, agent)
	case agentsv1alpha1.AgentModeDaemon:
		result, reconcileErr = r.reconcileDaemonRun(ctx, run, agent)
	default:
		r.setRunFailedStatus(run, fmt.Sprintf("Unknown agent mode: %s", agent.Spec.Mode))
	}

	if reconcileErr != nil {
		modeSpan.RecordError(reconcileErr)
		modeSpan.SetStatus(codes.Error, "mode dispatch")
	}
	modeSpan.End()

	// Always patch status so Mode and Phase are persisted, even when the
	// reconcile function returns an error (e.g. Job creation failure).
	if err := patchStatus(ctx, r.Client, run, statusPatch); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "patch status")
		return ctrl.Result{}, err
	}

	if reconcileErr != nil {
		span.RecordError(reconcileErr)
		span.SetStatus(codes.Error, "reconcile")
		return ctrl.Result{}, reconcileErr
	}

	span.SetAttributes(attribute.String("agentrun.phase", string(run.Status.Phase)))

	return result, nil
}

// checkConcurrency enforces the agent's concurrency policy.
// Returns true if the run is allowed to proceed.
func (r *AgentRunReconciler) checkConcurrency(ctx context.Context, run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent, statusPatch client.Patch) (bool, error) {
	maxRuns := 1
	policy := "queue"
	if agent.Spec.Concurrency != nil {
		if agent.Spec.Concurrency.MaxRuns > 0 {
			maxRuns = agent.Spec.Concurrency.MaxRuns
		}
		if agent.Spec.Concurrency.Policy != "" {
			policy = agent.Spec.Concurrency.Policy
		}
	}

	// Count active runs for this agent
	runList := &agentsv1alpha1.AgentRunList{}
	if err := r.List(ctx, runList, client.InNamespace(run.Namespace), client.MatchingLabels{
		resources.LabelAgent: agent.Name,
	}); err != nil {
		return false, err
	}

	activeCount := 0
	for _, existing := range runList.Items {
		if existing.Name == run.Name {
			continue
		}
		if existing.Status.Phase == agentsv1alpha1.AgentRunPhaseRunning {
			activeCount++
		}
	}

	if activeCount >= maxRuns {
		switch policy {
		case "queue":
			if run.Status.Phase != agentsv1alpha1.AgentRunPhaseQueued {
				run.Status.Phase = agentsv1alpha1.AgentRunPhaseQueued
				if err := patchStatus(ctx, r.Client, run, statusPatch); err != nil {
					return false, err
				}
			}
			return false, nil

		case "reject":
			r.setRunFailedStatus(run, "Rejected: max concurrent runs exceeded")
			if err := patchStatus(ctx, r.Client, run, statusPatch); err != nil {
				return false, err
			}
			return false, nil

		case "replace":
			// Cancel the oldest running run
			if err := r.cancelOldestRun(ctx, run, runList); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	return true, nil
}

func (r *AgentRunReconciler) cancelOldestRun(ctx context.Context, current *agentsv1alpha1.AgentRun, runList *agentsv1alpha1.AgentRunList) error {
	var running []agentsv1alpha1.AgentRun
	for _, run := range runList.Items {
		if run.Name != current.Name && run.Status.Phase == agentsv1alpha1.AgentRunPhaseRunning {
			running = append(running, run)
		}
	}

	if len(running) == 0 {
		return nil
	}

	// Sort by creation time, oldest first
	sort.Slice(running, func(i, j int) bool {
		return running[i].CreationTimestamp.Before(&running[j].CreationTimestamp)
	})

	oldest := &running[0]
	patch := client.MergeFrom(oldest.DeepCopy())
	oldest.Status.Phase = agentsv1alpha1.AgentRunPhaseFailed
	now := metav1.Now()
	oldest.Status.CompletionTime = &now
	oldest.Status.Output = "Cancelled by replace policy"
	meta.SetStatusCondition(&oldest.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentRunConditionComplete,
		Status:  metav1.ConditionTrue,
		Reason:  "Cancelled",
		Message: fmt.Sprintf("Replaced by %s", current.Name),
	})

	return patchStatus(ctx, r.Client, oldest, patch)
}

// reconcileTaskRun handles task mode: create Job, poll status.
func (r *AgentRunReconciler) reconcileTaskRun(ctx context.Context, run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Resolve AgentTools for the job
	var agentTools []agentsv1alpha1.AgentTool
	for _, binding := range agent.Spec.Tools {
		tool := &agentsv1alpha1.AgentTool{}
		if err := r.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: agent.Namespace}, tool); err == nil {
			agentTools = append(agentTools, *tool)
		}
	}

	// Resolve Provider CRs for the job
	var resolvedProviders []agentsv1alpha1.Provider
	for _, ref := range agent.Spec.ProviderRefs {
		prov := &agentsv1alpha1.Provider{}
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: agent.Namespace}, prov); err == nil {
			resolvedProviders = append(resolvedProviders, *prov)
		}
	}

	// Check if Job already exists
	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: run.Name, Namespace: run.Namespace}, existingJob)

	if apierrors.IsNotFound(err) {
		// Resolve git workspace config if spec.git is set
		var gitCfg *resources.GitWorkspaceConfig
		if run.Spec.Git != nil {
			integration := &agentsv1alpha1.Integration{}
			if err := r.Get(ctx, types.NamespacedName{Name: run.Spec.Git.IntegrationRef, Namespace: run.Namespace}, integration); err != nil {
				log.Error(err, "Failed to resolve Integration for git workspace", "integrationRef", run.Spec.Git.IntegrationRef)
				r.setRunFailedStatus(run, fmt.Sprintf("Integration %q not found: %v", run.Spec.Git.IntegrationRef, err))
				return ctrl.Result{}, nil
			}
			resolved, err := resources.ResolveGitWorkspace(run.Spec.Git, integration)
			if err != nil {
				log.Error(err, "Failed to resolve git workspace config")
				r.setRunFailedStatus(run, fmt.Sprintf("git workspace config error: %v", err))
				return ctrl.Result{}, nil
			}
			gitCfg = resolved
		}

		// Create a per-run ConfigMap with git tool entries if needed
		runConfigMapName := ""
		if gitCfg != nil {
			// Get the base agent ConfigMap
			baseConfigMap := &corev1.ConfigMap{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      resources.ObjectName(agent.Name, "config"),
				Namespace: run.Namespace,
			}, baseConfigMap); err != nil {
				return ctrl.Result{}, fmt.Errorf("get base config: %w", err)
			}

			gitToolEntries := gitCfg.GitToolEntries()

			runCM, err := resources.BuildAgentRunConfigMap(baseConfigMap, run.Name, gitToolEntries)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("build run config: %w", err)
			}
			if err := controllerutil.SetControllerReference(run, runCM, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.Create(ctx, runCM); err != nil {
				return ctrl.Result{}, fmt.Errorf("create run config: %w", err)
			}
			runConfigMapName = runCM.Name
			log.Info("Created per-run ConfigMap with git tool entries", "configMap", runCM.Name)
		}

		// Create Job
		job := resources.BuildAgentRunJob(run, agent, agentTools, resolvedProviders, gitCfg, runConfigMapName, r.Infra)
		if err := controllerutil.SetControllerReference(run, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		// Add agent label for concurrency tracking
		if job.Labels == nil {
			job.Labels = make(map[string]string)
		}
		job.Labels[resources.LabelAgent] = agent.Name

		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}

		now := metav1.Now()
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseRunning
		run.Status.StartTime = &now
		run.Status.JobName = job.Name
		run.Status.Model = agent.Spec.Model

		log.Info("Created Job for AgentRun", "job", job.Name)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	// Job exists — check its status
	if existingJob.Status.Succeeded > 0 {
		// Parse output from pod termination message
		result := r.getJobOutput(ctx, existingJob)

		now := metav1.Now()
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
		run.Status.CompletionTime = &now
		run.Status.Output = result.Output
		if result.Model != "" {
			run.Status.Model = result.Model
		}
		if result.TraceID != "" {
			run.Status.TraceID = result.TraceID
		}
		run.Status.ToolCalls = result.Steps
		// NOTE: outcome (PR/issue/memory artifacts + intent + summary) is now
		// written directly to status.outcome by the executing agent via the
		// run_finish built-in tool in agentops-runtime. The controller no
		// longer parses git outputs from the termination log.
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:   agentsv1alpha1.AgentRunConditionComplete,
			Status: metav1.ConditionTrue,
			Reason: "Succeeded",
		})

		return ctrl.Result{}, nil
	}

	if existingJob.Status.Failed > 0 {
		// Try to get error from termination message
		result := r.getJobOutput(ctx, existingJob)

		now := metav1.Now()
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseFailed
		run.Status.CompletionTime = &now
		if result.Error != "" {
			run.Status.Output = result.Error
		} else {
			run.Status.Output = "Job failed"
		}
		if result.Model != "" {
			run.Status.Model = result.Model
		}
		if result.TraceID != "" {
			run.Status.TraceID = result.TraceID
		}
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentRunConditionComplete,
			Status:  metav1.ConditionTrue,
			Reason:  "Failed",
			Message: run.Status.Output,
		})

		return ctrl.Result{}, nil
	}

	// Still running
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// reconcileDaemonRun handles daemon mode: send prompt to agent HTTP endpoint.
func (r *AgentRunReconciler) reconcileDaemonRun(ctx context.Context, run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// If already running, we're waiting for completion
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseRunning {
		// Check agent status endpoint for completion
		return r.pollDaemonRunStatus(ctx, run, agent)
	}

	// Send prompt to agent
	serviceURL := resources.AgentServiceURL(agent)
	promptURL := fmt.Sprintf("%s/prompt", serviceURL)

	body, _ := json.Marshal(map[string]string{
		"prompt": run.Spec.Prompt,
	})

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, promptURL, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	// Inject trace context for cross-agent trace linking.
	// The daemon runtime reads the Traceparent header to create a span link.
	if tp, ok := run.Annotations["agents.agentops.io/traceparent"]; ok && tp != "" {
		httpReq.Header.Set("Traceparent", tp)
	}
	if parentAgent, ok := run.Annotations["agents.agentops.io/parent-agent"]; ok && parentAgent != "" {
		httpReq.Header.Set("X-AgentOps-Parent-Agent", parentAgent)
	}
	httpReq.Header.Set("X-AgentOps-Run-Name", run.Name)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		log.Error(err, "Failed to send prompt to agent", "url", promptURL)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
		r.setRunFailedStatus(run, fmt.Sprintf("Agent returned %d: %s", resp.StatusCode, string(respBody)))
		return ctrl.Result{}, nil
	}

	// Parse response
	var result struct {
		Output     string `json:"output"`
		ToolCalls  int    `json:"toolCalls"`
		TokensUsed int    `json:"tokensUsed"`
		Cost       string `json:"cost"`
		Model      string `json:"model"`
		TraceID    string `json:"traceID"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Non-blocking: response might be streamed
		log.V(1).Info("Could not decode response, marking as running")
	}

	now := metav1.Now()
	if result.Output != "" {
		// Synchronous completion
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
		run.Status.CompletionTime = &now
		run.Status.Output = result.Output
		run.Status.ToolCalls = result.ToolCalls
		run.Status.TokensUsed = result.TokensUsed
		run.Status.Cost = result.Cost
		run.Status.Model = result.Model
		if result.TraceID != "" {
			run.Status.TraceID = result.TraceID
		}
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:   agentsv1alpha1.AgentRunConditionComplete,
			Status: metav1.ConditionTrue,
			Reason: "Succeeded",
		})
	} else {
		// Async: mark as running, poll later
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseRunning
		run.Status.StartTime = &now
		run.Status.Model = agent.Spec.Model
	}

	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseRunning {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	log.Info("Daemon AgentRun completed", "output", run.Status.Output)
	return ctrl.Result{}, nil
}

// pollDaemonRunStatus checks the daemon agent's /status endpoint for run completion.
// Enforces a TTL based on the agent's Timeout field to prevent infinite polling.
func (r *AgentRunReconciler) pollDaemonRunStatus(ctx context.Context, run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent) (ctrl.Result, error) {
	// TTL check: fail the run if it's been running longer than the agent timeout.
	if run.Status.StartTime != nil {
		timeout := 30 * time.Minute // default
		if agent.Spec.Timeout != "" {
			if d, err := time.ParseDuration(agent.Spec.Timeout); err == nil && d > 0 {
				timeout = d
			}
		}
		elapsed := time.Since(run.Status.StartTime.Time)
		if elapsed > timeout {
			r.setRunFailedStatus(run, fmt.Sprintf("Run timed out after %s (limit: %s)", elapsed.Round(time.Second), timeout))
			return ctrl.Result{}, nil
		}
	}

	serviceURL := resources.AgentServiceURL(agent)
	statusURL := fmt.Sprintf("%s/status", serviceURL)

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// Agent might be temporarily unavailable
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	var status struct {
		Busy       bool   `json:"busy"`
		Output     string `json:"output"`
		ToolCalls  int    `json:"toolCalls"`
		TokensUsed int    `json:"tokensUsed"`
		Cost       string `json:"cost"`
		Model      string `json:"model"`
		TraceID    string `json:"traceID"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	if status.Busy {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Complete — caller will patch status
	now := metav1.Now()
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.CompletionTime = &now
	run.Status.Output = status.Output
	run.Status.ToolCalls = status.ToolCalls
	run.Status.TokensUsed = status.TokensUsed
	run.Status.Cost = status.Cost
	run.Status.Model = status.Model
	if status.TraceID != "" {
		run.Status.TraceID = status.TraceID
	}
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentRunConditionComplete,
		Status: metav1.ConditionTrue,
		Reason: "Succeeded",
	})

	return ctrl.Result{}, nil
}

// getJobOutput reads the output from a completed Job's pod termination message.
// The task runtime writes a JSON object to /dev/termination-log with fields:
// output, steps, model, success, error.
func (r *AgentRunReconciler) getJobOutput(ctx context.Context, job *batchv1.Job) taskRunOutput {
	result := taskRunOutput{Output: "(no output captured)"}

	// Find pods owned by the job
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(job.Namespace), client.MatchingLabels(
		labels.Set{"job-name": job.Name},
	)); err != nil {
		return result
	}

	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "agent-runtime" && cs.State.Terminated != nil {
				msg := cs.State.Terminated.Message
				if msg == "" {
					continue
				}

				// Try to parse as JSON (structured output from runtime)
				var parsed taskRunOutput
				if err := json.Unmarshal([]byte(msg), &parsed); err == nil && parsed.Output != "" {
					return parsed
				}

				// Fallback: treat as plain text output
				result.Output = msg
				return result
			}
		}
	}

	return result
}

// taskRunOutput matches the JSON the task runtime writes to /dev/termination-log.
// Outcome data (PR/issue/memory artifacts) is NOT carried here — the runtime
// patches status.outcome directly via the K8s API in the run_finish tool.
type taskRunOutput struct {
	Output  string `json:"output"`
	Steps   int    `json:"steps"`
	Model   string `json:"model"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
	TraceID string `json:"traceID,omitempty"`
}

// setRunFailedStatus sets the AgentRun status to Failed. Caller must patch status.
func (r *AgentRunReconciler) setRunFailedStatus(run *agentsv1alpha1.AgentRun, message string) {
	now := metav1.Now()
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseFailed
	run.Status.CompletionTime = &now
	run.Status.Output = message
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:    agentsv1alpha1.AgentRunConditionComplete,
		Status:  metav1.ConditionTrue,
		Reason:  "Failed",
		Message: message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.AgentRun{}).
		Owns(&batchv1.Job{}).
		Named("agentrun").
		Complete(r)
}
