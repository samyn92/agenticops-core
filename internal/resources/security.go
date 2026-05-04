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

// This file owns every security-related decision the operator makes when
// rendering Pods. The model is:
//
//  1. Restricted-by-default: every operator-built Pod begins life with the
//     pod- and container-level SecurityContext required to pass the upstream
//     Kubernetes "restricted" Pod Security Standard, plus a writable /tmp
//     emptyDir, drop-ALL capabilities, no privilege escalation, read-only
//     root filesystem, RunAsNonRoot, and a non-root UID.
//
//  2. Safety-floor merge: callers (Agent / AgentTool.MCPServer / Channel)
//     can supply a SecurityOverrides struct to relax non-security-relevant
//     fields (RunAsUser, FSGroup, SupplementalGroups, custom seccomp
//     profile path) but any field that would weaken the restricted profile
//     is silently clamped and reported via the returned violations slice.
//
//  3. Single entry point: every builder calls ApplySecurity() exactly once
//     against the finished PodSpec — there is no other place in the codebase
//     where SecurityContext is set. This is enforced by code review and by
//     the restricted-PSA test in security_test.go.

import (
	"fmt"

	agentsv1alpha1 "github.com/samyn92/agentops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// RestrictedRunAsUser is the default non-root UID used by every
// operator-built container when no explicit user is configured. It matches
// the distroless `nonroot` UID used by all of our images (mcp-gateway,
// token-injector, channel-*, agent-runtime-fantasy from v0.15.0).
const RestrictedRunAsUser int64 = 65532

// ====================================================================
// Defaults — the restricted floor
// ====================================================================

// RestrictedContainerSecurityContext returns the container-level
// SecurityContext that satisfies the Kubernetes "restricted" Pod Security
// Standard. Applied to every container and init container the operator
// emits.
//
// Properties:
//   - allowPrivilegeEscalation: false
//   - readOnlyRootFilesystem:   true   (a tmp emptyDir is mounted at /tmp)
//   - runAsNonRoot:             true
//   - capabilities:             drop ALL
//   - seccompProfile:           RuntimeDefault
func RestrictedContainerSecurityContext() *corev1.SecurityContext {
	f := false
	t := true
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &f,
		ReadOnlyRootFilesystem:   &t,
		RunAsNonRoot:             &t,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// RestrictedPodSecurityContext returns the pod-level SecurityContext that
// satisfies the Kubernetes "restricted" Pod Security Standard. It pins a
// non-root UID/GID and uses the runtime-default seccomp profile.
func RestrictedPodSecurityContext() *corev1.PodSecurityContext {
	t := true
	uid := RestrictedRunAsUser
	return &corev1.PodSecurityContext{
		RunAsNonRoot: &t,
		RunAsUser:    &uid,
		RunAsGroup:   &uid,
		FSGroup:      &uid,
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// ====================================================================
// Volumes
// ====================================================================

// TmpVolume returns the standard /tmp emptyDir Volume that is added to
// every operator-built PodSpec. Pair with TmpVolumeMount() on each
// container so they retain a writable /tmp under a read-only root
// filesystem.
func TmpVolume() corev1.Volume {
	return corev1.Volume{
		Name:         VolumeTmp,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
}

// TmpVolumeMount returns the standard /tmp VolumeMount.
func TmpVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: VolumeTmp, MountPath: MountTmp}
}

// ====================================================================
// Safety-floor merge
// ====================================================================

// mergePodSecurityContext layers user overrides on top of the restricted
// pod-level defaults and returns a new SecurityContext together with a
// list of human-readable violation strings describing every override that
// was clamped to the safety floor.
func mergePodSecurityContext(override *corev1.PodSecurityContext) (*corev1.PodSecurityContext, []string) {
	out := RestrictedPodSecurityContext()
	if override == nil {
		return out, nil
	}
	var violations []string

	// RunAsNonRoot: must remain true. Reject explicit false.
	if override.RunAsNonRoot != nil && !*override.RunAsNonRoot {
		violations = append(violations, "pod.runAsNonRoot=false rejected: must be true (restricted PSS)")
	}

	// RunAsUser: any non-zero UID is allowed. Reject 0.
	if override.RunAsUser != nil {
		if *override.RunAsUser == 0 {
			violations = append(violations, "pod.runAsUser=0 rejected: must be non-zero (restricted PSS)")
		} else {
			uid := *override.RunAsUser
			out.RunAsUser = &uid
		}
	}
	if override.RunAsGroup != nil {
		gid := *override.RunAsGroup
		out.RunAsGroup = &gid
	}
	if override.FSGroup != nil {
		fsg := *override.FSGroup
		out.FSGroup = &fsg
	}
	if len(override.SupplementalGroups) > 0 {
		out.SupplementalGroups = append([]int64(nil), override.SupplementalGroups...)
	}
	if override.FSGroupChangePolicy != nil {
		p := *override.FSGroupChangePolicy
		out.FSGroupChangePolicy = &p
	}
	// SeccompProfile: only allow RuntimeDefault or a Localhost profile.
	// Unconfined is forbidden.
	if override.SeccompProfile != nil {
		if override.SeccompProfile.Type == corev1.SeccompProfileTypeUnconfined {
			violations = append(violations, "pod.seccompProfile.type=Unconfined rejected: must be RuntimeDefault or Localhost (restricted PSS)")
		} else {
			sp := *override.SeccompProfile
			out.SeccompProfile = &sp
		}
	}
	// SELinuxOptions: pass through (not security-relevant for the restricted
	// profile beyond well-known unsafe types — leave to admission policy).
	if override.SELinuxOptions != nil {
		so := *override.SELinuxOptions
		out.SELinuxOptions = &so
	}

	return out, violations
}

// mergeContainerSecurityContext layers user overrides on top of the
// restricted container-level defaults. Hard-coded fields cannot be relaxed:
// privilege escalation, RO rootfs, RunAsNonRoot, dropping ALL caps, and the
// "no caps added" rule are enforced.
func mergeContainerSecurityContext(override *corev1.SecurityContext) (*corev1.SecurityContext, []string) {
	out := RestrictedContainerSecurityContext()
	if override == nil {
		return out, nil
	}
	var violations []string

	if override.AllowPrivilegeEscalation != nil && *override.AllowPrivilegeEscalation {
		violations = append(violations, "container.allowPrivilegeEscalation=true rejected (restricted PSS)")
	}
	if override.Privileged != nil && *override.Privileged {
		violations = append(violations, "container.privileged=true rejected (restricted PSS)")
	}
	if override.ReadOnlyRootFilesystem != nil && !*override.ReadOnlyRootFilesystem {
		violations = append(violations, "container.readOnlyRootFilesystem=false rejected (restricted PSS)")
	}
	if override.RunAsNonRoot != nil && !*override.RunAsNonRoot {
		violations = append(violations, "container.runAsNonRoot=false rejected (restricted PSS)")
	}
	if override.RunAsUser != nil {
		if *override.RunAsUser == 0 {
			violations = append(violations, "container.runAsUser=0 rejected (restricted PSS)")
		} else {
			uid := *override.RunAsUser
			out.RunAsUser = &uid
		}
	}
	if override.RunAsGroup != nil {
		gid := *override.RunAsGroup
		out.RunAsGroup = &gid
	}
	if override.Capabilities != nil {
		// Adds are forbidden under restricted (except NET_BIND_SERVICE per PSS,
		// but we are stricter — no adds at all to keep the surface minimal).
		if len(override.Capabilities.Add) > 0 {
			violations = append(violations,
				fmt.Sprintf("container.capabilities.add=%v rejected (restricted PSS — no adds permitted)",
					override.Capabilities.Add))
		}
		// We always drop ALL — extra drops are a no-op, ignore them.
	}
	if override.SeccompProfile != nil {
		if override.SeccompProfile.Type == corev1.SeccompProfileTypeUnconfined {
			violations = append(violations, "container.seccompProfile.type=Unconfined rejected (restricted PSS)")
		} else {
			sp := *override.SeccompProfile
			out.SeccompProfile = &sp
		}
	}
	if override.SELinuxOptions != nil {
		so := *override.SELinuxOptions
		out.SELinuxOptions = &so
	}
	return out, violations
}

// ====================================================================
// Apply
// ====================================================================

// ComputeSecurityViolations returns the same human-readable violations
// that ApplySecurity would emit for the given overrides, without rendering
// a pod spec. Controllers call this directly to populate the
// SecurityPolicyViolations condition on the owning resource.
func ComputeSecurityViolations(overrides *agentsv1alpha1.SecurityOverrides) []string {
	if overrides == nil {
		return nil
	}
	_, podViolations := mergePodSecurityContext(overrides.Pod)
	_, containerViolations := mergeContainerSecurityContext(overrides.Container)
	out := append([]string(nil), podViolations...)
	out = append(out, containerViolations...)
	return out
}

// ApplySecurity is the single entry point through which every builder
// finalises pod security. It:
//
//   - Sets podSpec.SecurityContext to the restricted defaults merged with
//     overrides.Pod (if any).
//   - Sets containerSecurityContext on every Container and InitContainer
//     to the restricted defaults; the container named mainContainerName
//     additionally receives overrides.Container merged on top.
//   - Adds a /tmp emptyDir volume and mounts it on every container that
//     does not already mount one.
//   - Sets podSpec.AutomountServiceAccountToken to false unless the
//     overrides explicitly opt in.
//
// The returned slice contains a human-readable description of every
// override that was clamped to the safety floor; the caller (controller)
// surfaces this on the owning CR's status.conditions.
func ApplySecurity(podSpec *corev1.PodSpec, mainContainerName string, overrides *agentsv1alpha1.SecurityOverrides) []string {
	var (
		podOverride       *corev1.PodSecurityContext
		containerOverride *corev1.SecurityContext
		automount         *bool
	)
	if overrides != nil {
		podOverride = overrides.Pod
		containerOverride = overrides.Container
		automount = overrides.AutomountServiceAccountToken
	}

	mergedPod, podViolations := mergePodSecurityContext(podOverride)
	mergedMain, mainViolations := mergeContainerSecurityContext(containerOverride)
	violations := append([]string(nil), podViolations...)
	violations = append(violations, mainViolations...)

	podSpec.SecurityContext = mergedPod

	// AutomountServiceAccountToken: default false; require explicit opt-in.
	if automount != nil && *automount {
		t := true
		podSpec.AutomountServiceAccountToken = &t
	} else {
		f := false
		podSpec.AutomountServiceAccountToken = &f
	}

	// Apply container SC on every init/main/sidecar.
	applyContainer := func(c *corev1.Container, override *corev1.SecurityContext) {
		if override == nil {
			c.SecurityContext = RestrictedContainerSecurityContext()
		} else {
			c.SecurityContext = override
		}
		// Ensure /tmp is mounted.
		hasTmp := false
		for _, vm := range c.VolumeMounts {
			if vm.Name == VolumeTmp {
				hasTmp = true
				break
			}
		}
		if !hasTmp {
			c.VolumeMounts = append(c.VolumeMounts, TmpVolumeMount())
		}
	}
	for i := range podSpec.InitContainers {
		applyContainer(&podSpec.InitContainers[i], nil)
	}
	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]
		if c.Name == mainContainerName {
			applyContainer(c, mergedMain)
		} else {
			applyContainer(c, nil)
		}
	}

	// Ensure tmp volume is present.
	hasTmpVol := false
	for _, v := range podSpec.Volumes {
		if v.Name == VolumeTmp {
			hasTmpVol = true
			break
		}
	}
	if !hasTmpVol {
		podSpec.Volumes = append(podSpec.Volumes, TmpVolume())
	}

	return violations
}
