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

package gce_pd

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/golang/glog"
	crdv1 "github.com/rootfs/snapshot/pkg/apis/crd/v1"
	"github.com/rootfs/snapshot/pkg/cloudprovider"
	"github.com/rootfs/snapshot/pkg/cloudprovider/providers/gce"
	"github.com/rootfs/snapshot/pkg/volume"
	"k8s.io/client-go/pkg/api/v1"
	k8svol "k8s.io/kubernetes/pkg/volume"
)

const (
	gcePersistentDiskPluginName = "gce-pd"
)

type gcePersistentDiskPlugin struct {
	cloud *gce.GCECloud
}

var _ volume.VolumePlugin = &gcePersistentDiskPlugin{}

func RegisterPlugin() volume.VolumePlugin {
	return &gcePersistentDiskPlugin{}
}

func GetPluginName() string {
	return gcePersistentDiskPluginName
}

func (plugin *gcePersistentDiskPlugin) Init(cloud cloudprovider.Interface) {
	plugin.cloud = cloud.(*gce.GCECloud)
}

func (plugin *gcePersistentDiskPlugin) SnapshotCreate(pv *v1.PersistentVolume) (*crdv1.VolumeSnapshotDataSource, error) {
	spec := &pv.Spec
	if spec == nil || spec.GCEPersistentDisk == nil {
		return nil, fmt.Errorf("invalid PV spec %v", spec)
	}
	diskName := spec.GCEPersistentDisk.PDName
	zone := pv.Labels[metav1.LabelZoneFailureDomain]
	snapshotName := createSnapshotName(string(pv.Name))
	glog.Infof("Jing snapshotName %s", snapshotName)
	// Gather provisioning options
	tags := make(map[string]string)
	//tags["kubernetes.io/created-for/snapshot/namespace"] = claim.Namespace
	//tags[CloudVolumeCreatedForClaimNameTag] = claim.Name
	//tags[CloudVolumeCreatedForVolumeNameTag] = pvName

	err := plugin.cloud.CreateSnapshot(diskName, zone, snapshotName, tags)
	if err != nil {
		return nil, err
	}
	return &crdv1.VolumeSnapshotDataSource{
		GCEPersistentDiskSnapshot: &crdv1.GCEPersistentDiskSnapshotSource{
			SnapshotName: snapshotName,
		},
	}, nil
}

func (plugin *gcePersistentDiskPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, pvc *v1.PersistentVolumeClaim, pvName string, parameters map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	var err error
	var tags = make(map[string]string)
	if snapshotData == nil || snapshotData.Spec.GCEPersistentDiskSnapshot == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	if pvc == nil {
		return nil, nil, fmt.Errorf("nil pvc")
	}

	snapId := snapshotData.Spec.GCEPersistentDiskSnapshot.SnapshotName
	//diskName := k8svol.GenerateVolumeName("pv-from-snapshot"+snapId, pvName, 255)
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

	err = plugin.cloud.CreateDiskFromSnapshot(snapId, diskName, diskType, zone, requestGB, tags)
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
	snapshotId := src.GCEPersistentDiskSnapshot.SnapshotName
	err := plugin.cloud.DeleteSnapshot(snapshotId)
	if err != nil {
		return err
	}

	return nil
}

func (plugin *gcePersistentDiskPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (isCompleted bool, err error) {
	if snapshotData == nil || snapshotData.Spec.GCEPersistentDiskSnapshot == nil {
		return false, fmt.Errorf("invalid VolumeSnapshotDataSource: %v", snapshotData)
	}
	snapshotId := snapshotData.Spec.GCEPersistentDiskSnapshot.SnapshotName
	return plugin.cloud.DescribeSnapshot(snapshotId)
}

func (plugin *gcePersistentDiskPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.GCEPersistentDisk == nil {
		return fmt.Errorf("Invalid GCE PD: %v", pv)
	}
	diskName := pv.Spec.GCEPersistentDisk.PDName
	return plugin.cloud.DeleteDisk(diskName)
}
