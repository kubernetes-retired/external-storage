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

package gluster

import (
	"fmt"
	"os/exec"

	"github.com/golang/glog"
	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
	"github.com/pborman/uuid"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	glusterfsBinary = "gluster"
	glusterfsEp     = "glusterfs-cluster"
)

type glusterfsPlugin struct {
}

var _ volume.Plugin = &glusterfsPlugin{}

// RegisterPlugin registers the volume plugin
func RegisterPlugin() volume.Plugin {
	return &glusterfsPlugin{}
}

// GetPluginName gets the name of the volume plugin
func GetPluginName() string {
	return "glusterfs"
}

func (h *glusterfsPlugin) Init(_ cloudprovider.Interface) {
}

func (h *glusterfsPlugin) SnapshotCreate(
	snapshot *crdv1.VolumeSnapshot,
	pv *v1.PersistentVolume,
	tags *map[string]string,
) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	if spec == nil || spec.Glusterfs == nil {
		return nil, nil, fmt.Errorf("invalid PV spec %v", spec)
	}

	volumePath := spec.Glusterfs.Path
	snapshotName := volumePath + "_" + uuid.New()
	cmd := exec.Command(glusterfsBinary, "snapshot", "create", snapshotName, volumePath, "no-timestamp")
	out, err := cmd.CombinedOutput()

	if err != nil {
		glog.Errorf("failed to create snapshot for volume :%v, out:%v, err: %v, command args: %s", volumePath, out, err, cmd.Args)
	}
	glog.V(1).Infof("snapshot %v created successfully", snapshotName)
	cmd = exec.Command(glusterfsBinary, "snapshot", "activate", snapshotName)
	_, err = cmd.CombinedOutput()

	if err != nil {
		glog.Errorf("failed to activate snapshot:%v , err: %s, snapshot command is %s", snapshotName, err, cmd.Args)
	}

	cond := []crdv1.VolumeSnapshotCondition{}
	if err == nil {
		cond = []crdv1.VolumeSnapshotCondition{
			{
				Status:             v1.ConditionTrue,
				Message:            "Snapshot created successfully",
				LastTransitionTime: metav1.Now(),
				Type:               crdv1.VolumeSnapshotConditionReady,
			},
		}
	} else {
		glog.V(2).Infof("failed to create snapshot, err: %v", err)
		cond = []crdv1.VolumeSnapshotCondition{
			{
				Status:             v1.ConditionTrue,
				Message:            fmt.Sprintf("Failed to create the snapshot: %v", err),
				LastTransitionTime: metav1.Now(),
				Type:               crdv1.VolumeSnapshotConditionError,
			},
		}
	}

	res := &crdv1.VolumeSnapshotDataSource{
		GlusterSnapshotVolume: &crdv1.GlusterVolumeSnapshotSource{
			SnapshotID: snapshotName,
		},
	}
	return res, &cond, err
}

func (h *glusterfsPlugin) SnapshotDelete(src *crdv1.VolumeSnapshotDataSource, _ *v1.PersistentVolume) error {
	var err error
	if src == nil || src.GlusterSnapshotVolume == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", src)
	}

	snapshotID := src.GlusterSnapshotVolume.SnapshotID
	glog.V(1).Infof("Received snapshot :%v delete request", snapshotID)
	cmd := exec.Command(glusterfsBinary, "--mode=script", "snapshot", "delete", snapshotID)
	output, err := cmd.CombinedOutput()

	if err != nil {
		glog.Errorf("failed to delete snapshot: %v, err: %v", snapshotID, err)
	}

	glog.V(1).Infof("snapshot deleted :%v successfully", string(output))
	return err
}

func (h *glusterfsPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error) {
	if snapshotData == nil || snapshotData.Spec.GlusterSnapshotVolume == nil {
		return nil, false, fmt.Errorf("failed to retrieve Snapshot spec")
	}

	snapshotID := snapshotData.Spec.GlusterSnapshotVolume.SnapshotID
	glog.V(1).Infof("received describe request on snapshot:%v", snapshotID)
	cmd := exec.Command(glusterfsBinary, "snapshot", "info", snapshotID)
	output, err := cmd.CombinedOutput()

	if err != nil {
		glog.Errorf("failed to describe snapshot:%v", snapshotID)
	}

	glog.V(1).Infof("snapshot details:%v", string(output))

	if len(snapshotData.Status.Conditions) == 0 {
		return nil, false, fmt.Errorf("No status condtions in VoluemSnapshotData for gluster snapshot type")
	}

	lastCondIdx := len(snapshotData.Status.Conditions) - 1
	retCondType := crdv1.VolumeSnapshotConditionError

	switch snapshotData.Status.Conditions[lastCondIdx].Type {
	case crdv1.VolumeSnapshotDataConditionReady:
		retCondType = crdv1.VolumeSnapshotConditionReady
	case crdv1.VolumeSnapshotDataConditionPending:
		retCondType = crdv1.VolumeSnapshotConditionPending
		// Error out.
	}
	retCond := []crdv1.VolumeSnapshotCondition{
		{
			Status:             snapshotData.Status.Conditions[lastCondIdx].Status,
			Message:            snapshotData.Status.Conditions[lastCondIdx].Message,
			LastTransitionTime: snapshotData.Status.Conditions[lastCondIdx].LastTransitionTime,
			Type:               retCondType,
		},
	}
	return &retCond, true, nil
}

// FindSnapshot finds a VolumeSnapshot by matching metadata
func (h *glusterfsPlugin) FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("FindSnapshot by tags: %#v", *tags)

	// TODO: Implement FindSnapshot
	return nil, nil, fmt.Errorf("Snapshot not found")
}

func (h *glusterfsPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, _ *v1.PersistentVolumeClaim, _ string, _ map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	// retrieve VolumeSnapshotDataSource
	if snapshotData == nil || snapshotData.Spec.GlusterSnapshotVolume == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}

	// restore snapshot to a PV
	snapID := snapshotData.Spec.GlusterSnapshotVolume.SnapshotID
	newSnapPV := snapID
	cmd := exec.Command(glusterfsBinary, "snapshot", "clone", newSnapPV, snapID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		glog.Errorf("snapshot :%v restore failed, err:%v", snapID, err)
		return nil, nil, fmt.Errorf("failed to restore %s, out: %v, err: %v", snapID, out, err)
	}

	glog.V(1).Infof("snapshot restored successfully to PV: %v", newSnapPV)

	pv := &v1.PersistentVolumeSource{
		Glusterfs: &v1.GlusterfsVolumeSource{
			Path:          newSnapPV,
			EndpointsName: glusterfsEp,
		},
	}
	return pv, nil, nil
}

func (h *glusterfsPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.Glusterfs == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", pv)
	}

	path := pv.Spec.Glusterfs.Path
	glog.Errorf("Going to delete volume with path:%v", path)
	//TODO: Delete this volume
	return nil
}
