/*
Copyright 2017 The Kubernetes Authors.

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
	"k8s.io/api/core/v1"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
)

// Plugin defines functions that should be implemented by the volume plugin
type Plugin interface {
	// Init inits volume plugin
	Init(cloudprovider.Interface)
	// SnapshotCreate creates a VolumeSnapshot from a PersistentVolumeSpec
	SnapshotCreate(*crdv1.VolumeSnapshot, *v1.PersistentVolume, *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error)
	// SnapshotDelete deletes a VolumeSnapshot
	// PersistentVolume is provided for volume types, if any, that need PV Spec to delete snapshot
	SnapshotDelete(*crdv1.VolumeSnapshotDataSource, *v1.PersistentVolume) error
	// SnapshotRestore restores (promotes) a volume snapshot into a volume
	SnapshotRestore(*crdv1.VolumeSnapshotData, *v1.PersistentVolumeClaim, string, map[string]string) (*v1.PersistentVolumeSource, map[string]string, error)
	// Describe an EBS volume snapshot status for create or delete.
	// return status (completed or pending or error), and error
	DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error)
	// FindSnapshot finds a VolumeSnapshot by matching metadata
	FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error)
	// VolumeDelete deletes a PV
	// TODO in the future pass kubernetes client for certain volumes (e.g. rbd) so they can access storage class to retrieve secret
	VolumeDelete(pv *v1.PersistentVolume) error
}
