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

package cinder

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/pkg/api/v1"

	"github.com/golang/glog"

	crdv1 "github.com/rootfs/snapshot/pkg/apis/crd/v1"
	"github.com/rootfs/snapshot/pkg/cloudprovider"
	"github.com/rootfs/snapshot/pkg/cloudprovider/providers/openstack"
	"github.com/rootfs/snapshot/pkg/volume"
	k8sVol "k8s.io/kubernetes/pkg/volume"
)

type cinderPlugin struct {
	cloud *openstack.OpenStack
}

var _ volume.VolumePlugin = &cinderPlugin{}

// Init inits volume plugin
func (c *cinderPlugin) Init(cloud cloudprovider.Interface) {
	c.cloud = cloud.(*openstack.OpenStack)
}

// RegisterPlugin creates an uninitialized cinder plugin
func RegisterPlugin() volume.VolumePlugin {
	return &cinderPlugin{}
}

// GetPluginName retrieves the name of the plugin
func GetPluginName() string {
	return "cinder"
}

// VolumeDelete deletes the specified volume pased on pv
func (c *cinderPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.Cinder == nil {
		return fmt.Errorf("invalid Cinder PV: %v", pv)
	}
	volumeID := pv.Spec.Cinder.VolumeID
	err := c.cloud.DeleteVolume(volumeID)
	if err != nil {
		return err
	}

	return nil
}

// SnapshotCreate creates a VolumeSnapshot from a PersistentVolumeSpec
func (c *cinderPlugin) SnapshotCreate(pv *v1.PersistentVolume) (*crdv1.VolumeSnapshotDataSource, error) {
	spec := &pv.Spec
	if spec == nil || spec.Cinder == nil {
		return nil, fmt.Errorf("invalid PV spec %v", spec)
	}
	volumeID := spec.Cinder.VolumeID
	snapshotName := string(pv.Name) + fmt.Sprintf("%d", time.Now().UnixNano())
	snapshotDescription := "kubernetes snapshot"
	tags := make(map[string]string)
	glog.Infof("issuing Cinder.CreateSnapshot - SourceVol: %s, Name: %s", volumeID, snapshotName)
	snapID, err := c.cloud.CreateSnapshot(volumeID, snapshotName, snapshotDescription, tags)
	if err != nil {
		return nil, err
	}

	return &crdv1.VolumeSnapshotDataSource{
		CinderSnapshot: &crdv1.CinderVolumeSnapshotSource{
			SnapshotID: snapID,
		},
	}, nil
}

// SnapshotDelete deletes a VolumeSnapshot
// PersistentVolume is provided for volume types, if any, that need PV Spec to delete snapshot
func (c *cinderPlugin) SnapshotDelete(src *crdv1.VolumeSnapshotDataSource, _ *v1.PersistentVolume) error {
	if src == nil || src.CinderSnapshot == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", src)
	}
	snapshotID := src.CinderSnapshot.SnapshotID
	err := c.cloud.DeleteSnapshot(snapshotID)
	if err != nil {
		return err
	}
	return nil
}

// SnapshotRestore creates a new Volume using the data on the specified Snapshot
func (c *cinderPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, pvc *v1.PersistentVolumeClaim, pvName string, parameters map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	var tags = make(map[string]string)
	var vType string
	var zone string

	if snapshotData == nil || snapshotData.Spec.CinderSnapshot == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	if pvc == nil {
		return nil, nil, fmt.Errorf("no pvc specified")
	}
	snapID := snapshotData.Spec.CinderSnapshot.SnapshotID
	volName := pvName
	capacity := pvc.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	requestedSz := capacity.Value()
	szGB := k8sVol.RoundUpSize(requestedSz, 1024*1024*1024)

	for k, v := range parameters {
		switch strings.ToLower(k) {
		case "type":
			vType = v
		case "zone":
			zone = v
		default:
			return nil, nil, fmt.Errorf("invalid option %q for volume plugin %s", k, GetPluginName())
		}
	}

	// FIXME(j-griffith): Should probably use int64 in gophercloud?
	volumeID, _, err := c.cloud.CreateVolume(volName, int(szGB), vType, zone, snapID, &tags)
	if err != nil {
		glog.Errorf("error create volume from snapshot: %v", err)
		return nil, nil, err
	}
	glog.V(2).Infof("Successfully created Cinder Volume from Snapshot, Volume: %s", volumeID)
	pv := &v1.PersistentVolumeSource{
		Cinder: &v1.CinderVolumeSource{
			VolumeID: volumeID,
			FSType:   "ext4",
			ReadOnly: false,
		},
	}
	return pv, nil, nil
}

// DescribeSnapshot retrieves info for the specified Snapshot
func (c *cinderPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (bool, error) {
	if snapshotData == nil || snapshotData.Spec.CinderSnapshot == nil {
		return false, fmt.Errorf("invalid VolumeSnapshotDataSource: %v", snapshotData)
	}
	snapshotID := snapshotData.Spec.CinderSnapshot.SnapshotID
	isComplete, err := c.cloud.DescribeSnapshot(snapshotID)
	if err != nil {
		return false, err
	}
	return isComplete, nil
}
