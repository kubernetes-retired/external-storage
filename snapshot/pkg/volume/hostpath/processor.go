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

package hostpath

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
)

const (
	depot        = "/tmp/"
	restorePoint = "/tmp/restore/"
)

type hostPathPlugin struct {
}

var _ volume.Plugin = &hostPathPlugin{}

// RegisterPlugin registers the volume plugin
func RegisterPlugin() volume.Plugin {
	return &hostPathPlugin{}
}

// GetPluginName gets the name of the volume plugin
func GetPluginName() string {
	return "hostPath"
}

func (h *hostPathPlugin) Init(_ cloudprovider.Interface) {
}

func (h *hostPathPlugin) SnapshotCreate(
	snapshot *crdv1.VolumeSnapshot,
	pv *v1.PersistentVolume,
	tags *map[string]string,
) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	if spec == nil || spec.HostPath == nil {
		return nil, nil, fmt.Errorf("invalid PV spec %v", spec)
	}
	path := spec.HostPath.Path
	file := depot + string(uuid.NewUUID()) + ".tgz"
	cmdline := []string{"tar", "czf", file, "-C", path, "."}
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	out, err := cmd.CombinedOutput()
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
		glog.V(2).Infof("failed to execute %q: %v", strings.Join(cmdline, " "), err)
		glog.V(3).Infof("output: %s", string(out))
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
		HostPath: &crdv1.HostPathVolumeSnapshotSource{
			Path: file,
		},
	}
	return res, &cond, err
}

func (h *hostPathPlugin) SnapshotDelete(src *crdv1.VolumeSnapshotDataSource, _ *v1.PersistentVolume) error {
	if src == nil || src.HostPath == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", src)
	}
	path := src.HostPath.Path
	return os.Remove(path)
}

func (h *hostPathPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error) {
	if snapshotData == nil || snapshotData.Spec.HostPath == nil {
		return nil, false, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	path := snapshotData.Spec.HostPath.Path
	if _, err := os.Stat(path); err != nil {
		return nil, false, err
	}
	if len(snapshotData.Status.Conditions) == 0 {
		return nil, false, fmt.Errorf("No status condtions in VoluemSnapshotData for hostpath snapshot type")
	}
	lastCondIdx := len(snapshotData.Status.Conditions) - 1
	retCondType := crdv1.VolumeSnapshotConditionError
	switch snapshotData.Status.Conditions[lastCondIdx].Type {
	case crdv1.VolumeSnapshotDataConditionReady:
		retCondType = crdv1.VolumeSnapshotConditionReady
	case crdv1.VolumeSnapshotDataConditionPending:
		retCondType = crdv1.VolumeSnapshotConditionPending
		// Error othewise
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
func (h *hostPathPlugin) FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("FindSnapshot by tags: %#v", *tags)

	// TODO: Implement FindSnapshot
	return &crdv1.VolumeSnapshotDataSource{
		HostPath: &crdv1.HostPathVolumeSnapshotSource{
			Path: "",
		},
	}, nil, nil
}

func (h *hostPathPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, _ *v1.PersistentVolumeClaim, _ string, _ map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	// retrieve VolumeSnapshotDataSource
	if snapshotData == nil || snapshotData.Spec.HostPath == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	// restore snapshot to a PV
	snapID := snapshotData.Spec.HostPath.Path
	dir := restorePoint + string(uuid.NewUUID())
	os.MkdirAll(dir, 0750)
	cmdline := []string{"tar", "xzf", snapID, "-C", dir, "--strip-components=1"}
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		glog.V(2).Infof("failed to execute %q: %v", strings.Join(cmdline, " "), err)
		glog.V(3).Infof("output: %s", string(out))
		return nil, nil, fmt.Errorf("failed to restore %s to %s: %v", snapID, dir, err)
	}
	pv := &v1.PersistentVolumeSource{
		HostPath: &v1.HostPathVolumeSource{
			Path: dir,
		},
	}
	return pv, nil, nil
}

func (h *hostPathPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.HostPath == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", pv)
	}
	path := pv.Spec.HostPath.Path
	return os.RemoveAll(path)
}
