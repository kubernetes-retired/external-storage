/*
Copyright 2016 Red Hat, Inc.

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

package volume

import (
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/unversioned"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// VolumeOptions contains option information about a volume
// https://github.com/kubernetes/kubernetes/blob/release-1.4/pkg/volume/plugins.go
type VolumeOptions struct {
	// Capacity is the size of a volume.
	Capacity resource.Quantity
	// AccessModes of a volume
	AccessModes []v1.PersistentVolumeAccessMode
	// Reclamation policy for a persistent volume
	PersistentVolumeReclaimPolicy v1.PersistentVolumeReclaimPolicy
	// PV.Name of the appropriate PersistentVolume. Used to generate cloud
	// volume name.
	PVName string
	// Volume provisioning parameters from StorageClass
	Parameters map[string]string
	// Volume selector from PersistentVolumeClaim
	Selector *unversioned.LabelSelector
}
