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

package awsebs

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kvol "k8s.io/kubernetes/pkg/volume/util"

	"github.com/golang/glog"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider/providers/aws"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
)

type awsEBSPlugin struct {
	cloud *aws.Cloud
}

var _ volume.Plugin = &awsEBSPlugin{}

// RegisterPlugin registers the volume plugin
func RegisterPlugin() volume.Plugin {
	return &awsEBSPlugin{}
}

// GetPluginName gets the name of the volume plugin
func GetPluginName() string {
	return "aws_ebs"
}

func (a *awsEBSPlugin) Init(cloud cloudprovider.Interface) {
	a.cloud = cloud.(*aws.Cloud)
}

func (a *awsEBSPlugin) SnapshotCreate(
	snapshot *crdv1.VolumeSnapshot,
	pv *v1.PersistentVolume,
	tags *map[string]string,
) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	if spec == nil || spec.AWSElasticBlockStore == nil {
		return nil, nil, fmt.Errorf("invalid PV spec %v", spec)
	}
	volumeID := spec.AWSElasticBlockStore.VolumeID
	if ind := strings.LastIndex(volumeID, "/"); ind >= 0 {
		volumeID = volumeID[(ind + 1):]
	}
	snapshotOpt := &aws.SnapshotOptions{
		VolumeID: volumeID,
		Tags:     tags,
	}
	// TODO: Convert AWS EBS snapshot status to crdv1.VolumeSnapshotCondition
	snapshotID, status, err := a.cloud.CreateSnapshot(snapshotOpt)
	if err != nil {
		return nil, nil, err
	}
	return &crdv1.VolumeSnapshotDataSource{
		AWSElasticBlockStore: &crdv1.AWSElasticBlockStoreVolumeSnapshotSource{
			SnapshotID: snapshotID,
			FSType:     spec.AWSElasticBlockStore.FSType,
		},
	}, convertAWSStatus(status), nil
}

func (a *awsEBSPlugin) SnapshotDelete(src *crdv1.VolumeSnapshotDataSource, _ *v1.PersistentVolume) error {
	if src == nil || src.AWSElasticBlockStore == nil {
		return fmt.Errorf("invalid VolumeSnapshotDataSource: %v", src)
	}
	snapshotID := src.AWSElasticBlockStore.SnapshotID
	_, err := a.cloud.DeleteSnapshot(snapshotID)
	glog.Infof("delete snapshot %s, err: %v", snapshotID, err)
	if err != nil {
		return err
	}

	return nil
}

func (a *awsEBSPlugin) DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error) {
	if snapshotData == nil || snapshotData.Spec.AWSElasticBlockStore == nil {
		return nil, false, fmt.Errorf("invalid VolumeSnapshotDataSource: %v", snapshotData)
	}
	snapshotID := snapshotData.Spec.AWSElasticBlockStore.SnapshotID
	status, isCompleted, err := a.cloud.DescribeSnapshot(snapshotID)
	return convertAWSStatus(status), isCompleted, err
}

// FindSnapshot finds a VolumeSnapshot by matching metadata
func (a *awsEBSPlugin) FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("FindSnapshot by tags: %#v", *tags)

	// TODO: Implement FindSnapshot
	return nil, nil, fmt.Errorf("Snapshot not found")
}

func (a *awsEBSPlugin) SnapshotRestore(snapshotData *crdv1.VolumeSnapshotData, pvc *v1.PersistentVolumeClaim, pvName string, parameters map[string]string) (*v1.PersistentVolumeSource, map[string]string, error) {
	var err error
	var tags = make(map[string]string)
	// retrieve VolumeSnapshotDataSource
	if snapshotData == nil || snapshotData.Spec.AWSElasticBlockStore == nil {
		return nil, nil, fmt.Errorf("failed to retrieve Snapshot spec")
	}
	if pvc == nil {
		return nil, nil, fmt.Errorf("nil pvc")
	}

	snapID := snapshotData.Spec.AWSElasticBlockStore.SnapshotID

	tags["Name"] = kvol.GenerateVolumeName("Created from snapshot "+snapID+" ", pvName, 255) // AWS tags can have 255 characters

	capacity := pvc.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	requestBytes := capacity.Value()
	// AWS works with gigabytes, convert to GiB with rounding up
	requestGB := int(kvol.RoundUpSize(requestBytes, 1024*1024*1024))
	volumeOptions := &aws.VolumeOptions{
		CapacityGB: requestGB,
		Tags:       tags,
		PVCName:    pvc.Name,
		SnapshotID: snapID,
	}
	// Apply Parameters (case-insensitive). We leave validation of
	// the values to the cloud provider.
	for k, v := range parameters {
		switch strings.ToLower(k) {
		case "type":
			volumeOptions.VolumeType = v
		case "zone":
			volumeOptions.AvailabilityZone = v
		case "iopspergb":
			volumeOptions.IOPSPerGB, err = strconv.Atoi(v)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid iopsPerGB value %q, must be integer between 1 and 30: %v", v, err)
			}
		case "encrypted":
			volumeOptions.Encrypted, err = strconv.ParseBool(v)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid encrypted boolean value %q, must be true or false: %v", v, err)
			}
		case "kmskeyid":
			volumeOptions.KmsKeyID = v
		default:
			return nil, nil, fmt.Errorf("invalid option %q", k)
		}
	}

	// TODO: implement PVC.Selector parsing
	if pvc.Spec.Selector != nil {
		return nil, nil, fmt.Errorf("claim.Spec.Selector is not supported for dynamic provisioning on AWS")
	}

	volumeID, err := a.cloud.CreateDisk(volumeOptions)
	if err != nil {
		glog.V(2).Infof("Error creating EBS Disk volume: %v", err)
		return nil, nil, err
	}
	glog.V(2).Infof("Successfully created EBS Disk volume %s", volumeID)

	labels, err := a.cloud.GetVolumeLabels(volumeID)
	if err != nil {
		// We don't really want to leak the volume here...
		glog.Errorf("error building labels for new EBS volume %q: %v", volumeID, err)
	}

	pv := &v1.PersistentVolumeSource{
		AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{
			VolumeID:  string(volumeID),
			Partition: 0,
			ReadOnly:  false,
		},
	}
	pv.AWSElasticBlockStore.FSType = snapshotData.Spec.AWSElasticBlockStore.FSType

	return pv, labels, nil

}

func (a *awsEBSPlugin) VolumeDelete(pv *v1.PersistentVolume) error {
	if pv == nil || pv.Spec.AWSElasticBlockStore == nil {
		return fmt.Errorf("invalid EBS PV: %v", pv)
	}
	volumeID := pv.Spec.AWSElasticBlockStore.VolumeID
	_, err := a.cloud.DeleteDisk(aws.KubernetesVolumeID(volumeID))
	if err != nil {
		return err
	}

	return nil
}

func convertAWSStatus(status string) *[]crdv1.VolumeSnapshotCondition {
	var snapConditions []crdv1.VolumeSnapshotCondition
	if strings.ToLower(status) == "completed" {
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionReady,
				Status:             v1.ConditionTrue,
				Message:            "Snapshot created successfully and it is ready",
				LastTransitionTime: metav1.Now(),
			},
		}
	} else if strings.ToLower(status) == "pending" {
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionPending,
				Status:             v1.ConditionUnknown,
				Message:            "Snapshot is being created",
				LastTransitionTime: metav1.Now(),
			},
		}
	} else {
		snapConditions = []crdv1.VolumeSnapshotCondition{
			{
				Type:               crdv1.VolumeSnapshotConditionError,
				Status:             v1.ConditionTrue,
				Message:            "Snapshot creation failed",
				LastTransitionTime: metav1.Now(),
			},
		}
	}

	return &snapConditions
}
