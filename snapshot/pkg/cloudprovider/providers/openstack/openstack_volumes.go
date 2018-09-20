/*
Copyright 2016 The Kubernetes Authors.

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

package openstack

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path"
	"strings"

	k8sVolume "k8s.io/kubernetes/pkg/volume"

	"github.com/gophercloud/gophercloud"
	volumesV2 "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/volumeattach"

	"github.com/golang/glog"
)

type volumeService interface {
	createVolume(opts VolumeCreateOpts) (string, string, error)
	getVolume(volumeID string) (Volume, error)
	deleteVolume(volumeName string) error
}

// VolumesV1 implementation for v1
type VolumesV1 struct {
	blockstorage *gophercloud.ServiceClient
	opts         BlockStorageOpts
}

// VolumesV2 implementation for v2
type VolumesV2 struct {
	blockstorage *gophercloud.ServiceClient
	opts         BlockStorageOpts
}

// Volume reprsents the Cinder Volume object
type Volume struct {
	// AttachedServerID is a UUID representing the Server Volume is attached to
	AttachedServerID string
	// AttachedDevice is device file path
	AttachedDevice string
	// ID is the volumes Cinder UUID.
	ID string
	// Name is a Human-readable display name for the volume.
	Name string
	// Status is the current state of the volume.
	Status string
}

// VolumeCreateOpts valid options for creating a Volume
type VolumeCreateOpts struct {
	Size             int
	Availability     string
	Name             string
	VolumeType       string
	Metadata         map[string]string
	SourceVolumeID   string
	SourceSnapshotID string
}

// Valid Volume status strings
const (
	VolumeAvailableStatus = "available"
	VolumeInUseStatus     = "in-use"
	VolumeDeletedStatus   = "deleted"
	VolumeErrorStatus     = "error"
)

func (volumes *VolumesV2) createVolume(opts VolumeCreateOpts) (string, string, error) {

	createOpts := volumesV2.CreateOpts{
		Name:             opts.Name,
		Size:             opts.Size,
		VolumeType:       opts.VolumeType,
		AvailabilityZone: opts.Availability,
		Metadata:         opts.Metadata,
		SnapshotID:       opts.SourceSnapshotID,
	}

	vol, err := volumesV2.Create(volumes.blockstorage, createOpts).Extract()
	if err != nil {
		return "", "", err
	}
	return vol.ID, vol.AvailabilityZone, nil
}

func (volumes *VolumesV2) getVolume(volumeID string) (Volume, error) {
	volumeV2, err := volumesV2.Get(volumes.blockstorage, volumeID).Extract()
	if err != nil {
		glog.Errorf("Error occurred getting volume by ID: %s", volumeID)
		return Volume{}, err
	}

	volume := Volume{
		ID:     volumeV2.ID,
		Name:   volumeV2.Name,
		Status: volumeV2.Status,
	}

	if len(volumeV2.Attachments) > 0 {
		volume.AttachedServerID = volumeV2.Attachments[0].ServerID
		volume.AttachedDevice = volumeV2.Attachments[0].Device
	}

	return volume, nil
}

func (volumes *VolumesV2) deleteVolume(volumeID string) error {
	err := volumesV2.Delete(volumes.blockstorage, volumeID).ExtractErr()
	if err != nil {
		glog.Errorf("Cannot delete volume %s: %v", volumeID, err)
	}

	return err
}

// OperationPending checks status, makes sure we're not in error state
func (os *OpenStack) OperationPending(diskName string) (bool, string, error) {
	volume, err := os.getVolume(diskName)
	if err != nil {
		return false, "", err
	}
	volumeStatus := volume.Status
	if volumeStatus == VolumeErrorStatus {
		glog.Errorf("status of volume %s is %s", diskName, volumeStatus)
		return false, volumeStatus, nil
	}
	if volumeStatus == VolumeAvailableStatus || volumeStatus == VolumeInUseStatus || volumeStatus == VolumeDeletedStatus {
		return false, volume.Status, nil
	}
	return true, volumeStatus, nil
}

// AttachDisk attaches specified cinder volume to the compute running kubelet
func (os *OpenStack) AttachDisk(instanceID, volumeID string) (string, error) {
	volume, err := os.getVolume(volumeID)
	if err != nil {
		return "", err
	}
	if volume.Status != VolumeAvailableStatus {
		errmsg := fmt.Sprintf("volume %s status is %s, not %s, can not be attached to instance %s.", volume.Name, volume.Status, VolumeAvailableStatus, instanceID)
		glog.Error(errmsg)
		return "", errors.New(errmsg)
	}
	cClient, err := os.NewComputeV2()
	if err != nil {
		return "", err
	}

	if volume.AttachedServerID != "" {
		if instanceID == volume.AttachedServerID {
			glog.V(4).Infof("Disk %s is already attached to instance %s", volumeID, instanceID)
			return volume.ID, nil
		}
		glog.V(2).Infof("Disk %s is attached to a different instance (%s), detaching", volumeID, volume.AttachedServerID)
		err = os.DetachDisk(volume.AttachedServerID, volumeID)
		if err != nil {
			return "", err
		}
	}

	// add read only flag here if possible spothanis
	_, err = volumeattach.Create(cClient, instanceID, &volumeattach.CreateOpts{
		VolumeID: volume.ID,
	}).Extract()
	if err != nil {
		glog.Errorf("Failed to attach %s volume to %s compute: %v", volumeID, instanceID, err)
		return "", err
	}
	glog.V(2).Infof("Successfully attached %s volume to %s compute", volumeID, instanceID)
	return volume.ID, nil
}

// DetachDisk detaches given cinder volume from the compute running kubelet
func (os *OpenStack) DetachDisk(instanceID, volumeID string) error {
	volume, err := os.getVolume(volumeID)
	if err != nil {
		return err
	}
	if volume.Status != VolumeInUseStatus {
		errmsg := fmt.Sprintf("can not detach volume %s, its status is %s.", volume.Name, volume.Status)
		glog.Error(errmsg)
		return errors.New(errmsg)
	}
	cClient, err := os.NewComputeV2()
	if err != nil {
		return err
	}
	if volume.AttachedServerID != instanceID {
		errMsg := fmt.Sprintf("Disk: %s has no attachments or is not attached to compute: %s", volume.Name, instanceID)
		glog.Error(errMsg)
		return errors.New(errMsg)
	}

	// This is a blocking call and effects kubelet's performance directly.
	// We should consider kicking it out into a separate routine, if it is bad.
	err = volumeattach.Delete(cClient, instanceID, volume.ID).ExtractErr()
	if err != nil {
		glog.Errorf("Failed to delete volume %s from compute %s attached %v", volume.ID, instanceID, err)
		return err
	}
	glog.V(2).Infof("Successfully detached volume: %s from compute: %s", volume.ID, instanceID)
	return nil
}

// Retrieves Volume by its ID.
func (os *OpenStack) getVolume(volumeID string) (Volume, error) {
	volumes, err := os.volumeService("")
	if err != nil || volumes == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return Volume{}, err
	}
	return volumes.getVolume(volumeID)
}

// CreateVolume of given size (in GiB)
func (os *OpenStack) CreateVolume(name string, size int, vtype, availability, snapshotID string, tags *map[string]string) (string, string, error) {
	volumes, err := os.volumeService("")
	if err != nil || volumes == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return "", "", err
	}

	opts := VolumeCreateOpts{
		Name:             name,
		Size:             size,
		VolumeType:       vtype,
		Availability:     availability,
		SourceSnapshotID: snapshotID,
	}
	if tags != nil {
		opts.Metadata = *tags
	}

	volumeID, volumeAZ, err := volumes.createVolume(opts)

	if err != nil {
		glog.Errorf("Failed to create a %d GB volume: %v", size, err)
		return "", "", err
	}

	glog.Infof("Created volume %v in Availability Zone: %v", volumeID, volumeAZ)
	return volumeID, volumeAZ, nil
}

// GetDevicePath returns the path of an attached block storage volume, specified by its id.
func (os *OpenStack) GetDevicePath(volumeID string) string {
	// Build a list of candidate device paths
	candidateDeviceNodes := []string{
		// KVM
		fmt.Sprintf("virtio-%s", volumeID[:20]),
		// KVM virtio-scsi
		fmt.Sprintf("scsi-0QEMU_QEMU_HARDDISK_%s", volumeID[:20]),
		// ESXi
		fmt.Sprintf("wwn-0x%s", strings.Replace(volumeID, "-", "", -1)),
	}

	files, _ := ioutil.ReadDir("/dev/disk/by-id/")

	for _, f := range files {
		for _, c := range candidateDeviceNodes {
			if c == f.Name() {
				glog.V(4).Infof("Found disk attached as %q; full devicepath: %s\n", f.Name(), path.Join("/dev/disk/by-id/", f.Name()))
				return path.Join("/dev/disk/by-id/", f.Name())
			}
		}
	}

	glog.Warningf("Failed to find device for the volumeID: %q\n", volumeID)
	return ""
}

// DeleteVolume deletes the specified volume
func (os *OpenStack) DeleteVolume(volumeID string) error {
	used, err := os.diskIsUsed(volumeID)
	if err != nil {
		return err
	}
	if used {
		msg := fmt.Sprintf("Cannot delete the volume %q, it's still attached to a node", volumeID)
		return k8sVolume.NewDeletedVolumeInUseError(msg)
	}

	volumes, err := os.volumeService("")
	if err != nil || volumes == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return err
	}

	err = volumes.deleteVolume(volumeID)
	if err != nil {
		glog.Errorf("Cannot delete volume %s: %v", volumeID, err)
	}
	return nil

}

// GetAttachmentDiskPath retrieves device path of attached volume to the compute running kubelet, as known by cinder
func (os *OpenStack) GetAttachmentDiskPath(instanceID, volumeID string) (string, error) {
	// See issue #33128 - Cinder does not always tell you the right device path, as such
	// we must only use this value as a last resort.
	volume, err := os.getVolume(volumeID)
	if err != nil {
		return "", err
	}
	if volume.Status != VolumeInUseStatus {
		errmsg := fmt.Sprintf("can not get device path of volume %s, its status is %s.", volume.Name, volume.Status)
		glog.Error(errmsg)
		return "", errors.New(errmsg)
	}
	if volume.AttachedServerID != "" {
		if instanceID == volume.AttachedServerID {
			// Attachment[0]["device"] points to the device path
			// see http://developer.openstack.org/api-ref-blockstorage-v1.html
			return volume.AttachedDevice, nil
		}
		errMsg := fmt.Sprintf("Disk %q is attached to a different compute: %q, should be detached before proceeding", volumeID, volume.AttachedServerID)
		glog.Error(errMsg)
		return "", errors.New(errMsg)
	}
	return "", fmt.Errorf("volume %s has no ServerId", volumeID)
}

// DiskIsAttached query if a volume is attached to a compute instance
func (os *OpenStack) DiskIsAttached(instanceID, volumeID string) (bool, error) {
	volume, err := os.getVolume(volumeID)
	if err != nil {
		return false, err
	}

	return instanceID == volume.AttachedServerID, nil
}

// DisksAreAttached query if a list of volumes are attached to a compute instance
func (os *OpenStack) DisksAreAttached(instanceID string, volumeIDs []string) (map[string]bool, error) {
	attached := make(map[string]bool)
	for _, volumeID := range volumeIDs {
		isAttached, _ := os.DiskIsAttached(instanceID, volumeID)
		attached[volumeID] = isAttached
	}
	return attached, nil
}

// diskIsUsed returns true a disk is attached to any node.
func (os *OpenStack) diskIsUsed(volumeID string) (bool, error) {
	volume, err := os.getVolume(volumeID)
	if err != nil {
		return false, err
	}
	return volume.AttachedServerID != "", nil
}

// ShouldTrustDevicePath query if we should trust the cinder provide deviceName, See issue #33128
func (os *OpenStack) ShouldTrustDevicePath() bool {
	return os.bsOpts.TrustDevicePath
}
