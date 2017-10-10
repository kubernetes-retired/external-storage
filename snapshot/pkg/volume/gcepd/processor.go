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

package gcepd

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider/providers/gce"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet/apis"
	k8svol "k8s.io/kubernetes/pkg/volume"
)

const (
	gcePersistentDiskPluginName = "gce-pd"
)

type gcePersistentDiskPlugin struct {
	cloud *gce.Cloud
}

var _ volume.Plugin = &gcePersistentDiskPlugin{}

// RegisterPlugin registers the volume plugin
func RegisterPlugin() volume.Plugin {
	return &gcePersistentDiskPlugin{}
}

// GetPluginName gets the name of the volume plugin
func GetPluginName() string {
	return gcePersistentDiskPluginName
}

func (plugin *gcePersistentDiskPlugin) Init(cloud cloudprovider.Interface) {
	plugin.cloud = cloud.(*gce.Cloud)
}

func (plugin *gcePersistentDiskPlugin) SnapshotCreate(pv *v1.PersistentVolume, tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	if spec == nil || spec.GCEPersistentDisk == nil {
		return nil, nil, fmt.Errorf("invalid PV spec %v", spec)
	}
	diskName := spec.GCEPersistentDisk.PDName
	zone := pv.Labels[apis.LabelZoneFailureDomain]
	snapshotName := createSnapshotName(string(pv.Name))

	err := plugin.cloud.CreateSnapshot(diskName, zone, snapshotName, *tags)
	if err != nil {
		return nil, nil, err
	}
	initConditions := []crdv1.VolumeSnapshotCondition{
		{
			Type:               crdv1.VolumeSnapshotConditionPending,
			Status:             v1.ConditionUnknown,
			Message:            "Snapshot creation is triggered",
			LastTransitionTime: metav1.Now(),
		},
	}
	return &crdv1.VolumeSnapshotDataSource{
		GCEPersistentDiskSnapshot: &crdv1.GCEPersistentDiskSnapshotSource{
			SnapshotName: snapshotName,
		},
	}, &initConditions, nil
}

func (plugin *gcePersistentDiskPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, pvc *v1.PersistentVolumeClaim, pvName string, parameters map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	var err error
	var tags = make(map[string]string)
	if snapshotData == nil || snapshotData.Spec.GCEPersistentDiskSnapshot == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	if pvc == nil {
		return nil, nil, fmt.Errorf("pvc is nil")
	}

	snapID := snapshotData.Spec.GCEPersistentDiskSnapshot.SnapshotName
	//diskName := k8svol.GenerateVolumeName("pv-from-snapshot"+snapID, pvName, 255)
	diskName := pvName
	capacity := pvc.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	requestBytes := capacity.Value()
	// GCE works with gigabytes, convert to GiB with rounding up
	requestGB := k8svol.RoundUpSize(requestBytes, 1024*1024*1024)

	// Apply Parameters (case-insensitive). We leave validation of
	// the values to the cloud provider.
	diskType := ""
	zone := ""
	for k, v := range parameters {
		switch strings.ToLower(k) {
		case "type":
			diskType = v
		case "zone":
			zone = v
		default:
			return nil, nil, fmt.Errorf("invalid option %q for volume plugin %s", k, GetPluginName())
		}
	}

	if zone == "" {
		// No zone specified, choose one randomly in the same region as the
		// node is running.
		zones, err := plugin.cloud.GetAllZones()
		if err != nil {
			glog.Infof("error getting zone information from GCE: %v", err)
			return nil, nil, err
		}
		zone = k8svol.ChooseZoneForVolume(zones, pvc.Name)
	}
	tags["source"] = k8svol.GenerateVolumeName("Created from snapshot "+snapID+" ", pvName, 255)
	glog.Infof("Provisioning disk %s from snapshot %s, zone %s requestGB %d tags %v", diskName, snapID, zone, requestGB, tags)
	err = plugin.cloud.CreateDiskFromSnapshot(snapID, diskName, diskType, zone, requestGB, tags)
	if err != nil {
		glog.Infof("Error creating GCE PD volume: %v", err)
		return nil, nil, err
	}
	glog.Infof("Successfully created GCE PD volume %s", diskName)

	labels, err := plugin.cloud.GetAutoLabelsForPD(diskName, zone)
	if err != nil {
		// We don't really want to leak the volume here...
		glog.Errorf("error getting labels for volume %q: %v", diskName, err)
	}

	pv := &v1.PersistentVolumeSource{
		GCEPersistentDisk: &v1.GCEPersistentDiskVolumeSource{
			PDName:    diskName,
			FSType:    "ext4",
			Partition: 0,
			ReadOnly:  false,
		},
	}
	return pv, labels, nil

}

func createSnapshotName(pvName string) string {
	name := pvName + fmt.Sprintf("%d", time.Now().UnixNano())
	return name
}

func (plugin *gcePersistentDiskPlugin) SnapshotDelete(src *crdv1.VolumeSnapshotDataSource, _ *v1.PersistentVolume) error {
	if src == nil || src.GCEPersistentDiskSnapshot == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", src)
	}
	snapshotID := src.GCEPersistentDiskSnapshot.SnapshotName
	err := plugin.cloud.DeleteSnapshot(snapshotID)
	if err != nil {
		return err
	}

	return nil
}

func (plugin *gcePersistentDiskPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error) {
	if snapshotData == nil || snapshotData.Spec.GCEPersistentDiskSnapshot == nil {
		return nil, false, fmt.Errorf("invalid VolumeSnapshotDataSource: %v", snapshotData)
	}
	snapshotID := snapshotData.Spec.GCEPersistentDiskSnapshot.SnapshotName
	status, isCompleted, err := plugin.cloud.DescribeSnapshot(snapshotID)
	return convertGCEStatus(status), isCompleted, err
}

// FindSnapshot finds a VolumeSnapshot by matching metadata
func (plugin *gcePersistentDiskPlugin) FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("FindSnapshot by tags: %#v", *tags)

	// TODO: Implement FindSnapshot
	return &crdv1.VolumeSnapshotDataSource{
		GCEPersistentDiskSnapshot: &crdv1.GCEPersistentDiskSnapshotSource{
			SnapshotName: "",
		},
	}, nil, nil
}

func (plugin *gcePersistentDiskPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.GCEPersistentDisk == nil {
		return fmt.Errorf("Invalid GCE PD: %v", pv)
	}
	diskName := pv.Spec.GCEPersistentDisk.PDName
	return plugin.cloud.DeleteDisk(diskName)
}

func convertGCEStatus(status string) *[]crdv1.VolumeSnapshotCondition {
	var snapConditions []crdv1.VolumeSnapshotCondition

	switch status {
	case "READY":
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionReady,
				Status:             v1.ConditionTrue,
				Message:            "Snapshot created successfully and it is ready",
				LastTransitionTime: metav1.Now(),
			},
		}
	case "FAILED":
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionError,
				Status:             v1.ConditionTrue,
				Message:            "Snapshot creation failed",
				LastTransitionTime: metav1.Now(),
			},
		}
	case "UPLOADING":
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionPending,
				Status:             v1.ConditionTrue,
				Message:            "Snapshot is uploading",
				LastTransitionTime: metav1.Now(),
			},
		}
	case "CREATING":
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionPending,
				Status:             v1.ConditionUnknown,
				Message:            "Snapshot is creating",
				LastTransitionTime: metav1.Now(),
			},
		}
	case "Deleting":
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionReady,
				Status:             v1.ConditionUnknown,
				Message:            "Snapshot is deleting",
				LastTransitionTime: metav1.Now(),
			},
		}

	}
	return &snapConditions

}
