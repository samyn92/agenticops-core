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
	agentsv1alpha1 "github.com/samyn92/agenticops-core/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildAgentPVC creates a PersistentVolumeClaim for a daemon agent.
func BuildAgentPVC(agent *agentsv1alpha1.Agent) *corev1.PersistentVolumeClaim {
	pvcName := ObjectName(agent.Name, "storage")

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: agent.Namespace,
			Labels:    CommonLabels(agent.Name, "storage"),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(agent.Spec.Storage.Size),
				},
			},
		},
	}

	if agent.Spec.Storage.StorageClass != "" {
		pvc.Spec.StorageClassName = &agent.Spec.Storage.StorageClass
	}

	return pvc
}
