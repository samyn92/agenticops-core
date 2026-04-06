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

	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	"github.com/samyn92/agenticops-core/internal/resources"
)

// AgentRunReconciler reconciles an AgentRun object.
type AgentRunReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agentruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agentruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.agenticops.io,resources=mcpservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;pods/log,verbs=get;list;watch

func (r *AgentRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch AgentRun
	run := &agentsv1alpha1.AgentRun{}
	if err := r.Get(ctx, req.NamespacedName, run); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip terminal states
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		run.Status.Phase == agentsv1alpha1.AgentRunPhaseFailed {
		return ctrl.Result{}, nil
	}

	// Save a copy for status patch comparison
	statusPatch := client.MergeFrom(run.DeepCopy())

	log.Info("Reconciling AgentRun", "name", run.Name, "phase", run.Status.Phase)

	// Resolve the target Agent
	agent := &agentsv1alpha1.Agent{}
	if err := r.Get(ctx, types.NamespacedName{Name: run.Spec.AgentRef, Namespace: run.Namespace}, agent); err != nil {
		r.setRunFailedStatus(run, fmt.Sprintf("Agent %q not found", run.Spec.AgentRef))
		if patchErr := patchStatus(ctx, r.Client, run, statusPatch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Set the mode from the agent
	run.Status.Mode = agent.Spec.Mode

	// Check concurrency
	allowed, err := r.checkConcurrency(ctx, run, agent, statusPatch)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !allowed {
		// Already set to Queued or Failed and patched
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// Execute based on agent mode
	var result ctrl.Result
	var reconcileErr error

	switch agent.Spec.Mode {
	case agentsv1alpha1.AgentModeTask:
		result, reconcileErr = r.reconcileTaskRun(ctx, run, agent)
	case agentsv1alpha1.AgentModeDaemon:
		result, reconcileErr = r.reconcileDaemonRun(ctx, run, agent)
	default:
		r.setRunFailedStatus(run, fmt.Sprintf("Unknown agent mode: %s", agent.Spec.Mode))
	}

	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	// Patch status (only writes if status actually changed)
	if err := patchStatus(ctx, r.Client, run, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

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

	// Resolve MCP servers for the job
	var mcpServers []agentsv1alpha1.MCPServer
	for _, binding := range agent.Spec.MCPServers {
		mcp := &agentsv1alpha1.MCPServer{}
		if err := r.Get(ctx, types.NamespacedName{Name: binding.Name, Namespace: agent.Namespace}, mcp); err == nil {
			mcpServers = append(mcpServers, *mcp)
		}
	}

	// Check if Job already exists
	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: run.Name, Namespace: run.Namespace}, existingJob)

	if apierrors.IsNotFound(err) {
		// Create Job
		job := resources.BuildAgentRunJob(run, agent, mcpServers)
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
		// Parse output from pod logs
		output := r.getJobOutput(ctx, existingJob)

		now := metav1.Now()
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
		run.Status.CompletionTime = &now
		run.Status.Output = output
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:   agentsv1alpha1.AgentRunConditionComplete,
			Status: metav1.ConditionTrue,
			Reason: "Succeeded",
		})

		return ctrl.Result{}, nil
	}

	if existingJob.Status.Failed > 0 {
		now := metav1.Now()
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseFailed
		run.Status.CompletionTime = &now
		run.Status.Output = "Job failed"
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:    agentsv1alpha1.AgentRunConditionComplete,
			Status:  metav1.ConditionTrue,
			Reason:  "Failed",
			Message: "Job execution failed",
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

	resp, err := httpClient.Post(promptURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Error(err, "Failed to send prompt to agent", "url", promptURL)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
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
func (r *AgentRunReconciler) pollDaemonRunStatus(ctx context.Context, run *agentsv1alpha1.AgentRun, agent *agentsv1alpha1.Agent) (ctrl.Result, error) {
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
	defer resp.Body.Close()

	var status struct {
		Busy       bool   `json:"busy"`
		Output     string `json:"output"`
		ToolCalls  int    `json:"toolCalls"`
		TokensUsed int    `json:"tokensUsed"`
		Cost       string `json:"cost"`
		Model      string `json:"model"`
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
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:   agentsv1alpha1.AgentRunConditionComplete,
		Status: metav1.ConditionTrue,
		Reason: "Succeeded",
	})

	return ctrl.Result{}, nil
}

// getJobOutput reads the output from a completed Job's pod logs.
func (r *AgentRunReconciler) getJobOutput(ctx context.Context, job *batchv1.Job) string {
	// Find pods owned by the job
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(job.Namespace), client.MatchingLabels(
		labels.Set{"job-name": job.Name},
	)); err != nil {
		return "(could not read pod logs)"
	}

	// In a real implementation, you'd read pod logs via the Kubernetes API.
	// For now, the task runtime writes structured JSON to stdout that the
	// operator can parse from the pod's termination message.
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "agent-runtime" && cs.State.Terminated != nil {
				if cs.State.Terminated.Message != "" {
					return cs.State.Terminated.Message
				}
			}
		}
	}

	return "(no output captured)"
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
