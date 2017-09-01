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
	"fmt"

	"github.com/gophercloud/gophercloud"
	snapshotsV2 "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/snapshots"

	"github.com/golang/glog"
)

// SnapshotsV2 is the Cinder V2 Snapshot service from gophercloud
type SnapshotsV2 struct {
	blockstorage *gophercloud.ServiceClient
	opts         BlockStorageOpts
}

// Snapshot is the representation of the Cinder Snapshot object
type Snapshot struct {
	ID             string
	Name           string
	Status         string
	SourceVolumeID string
}

// SnapshotCreateOpts are the valid create options for Cinder Snapshots
type SnapshotCreateOpts struct {
	VolumeID    string
	Name        string
	Description string
	Force       bool
	Metadata    map[string]string
}

type snapshotService interface {
	createSnapshot(opts SnapshotCreateOpts) (string, error)
	deleteSnapshot(snapshotName string) error
	getSnapshot(snapshotID string) (Snapshot, error)
}

func (snapshots *SnapshotsV2) createSnapshot(opts SnapshotCreateOpts) (string, error) {

	createOpts := snapshotsV2.CreateOpts{
		VolumeID:    opts.VolumeID,
		Force:       false,
		Name:        opts.Name,
		Description: opts.Description,
		Metadata:    opts.Metadata,
	}

	snap, err := snapshotsV2.Create(snapshots.blockstorage, createOpts).Extract()
	if err != nil {
		return "", err
	}
	return snap.ID, nil
}

func (snapshots *SnapshotsV2) getSnapshot(snapshotID string) (Snapshot, error) {
	var snap Snapshot
        glog.Infof("getSnapshot for snapshotID: %s.", snapshotID)
        snapshot, err := snapshotsV2.Get(snapshots.blockstorage, snapshotID).Extract()
        if err != nil {
                return snap, err
        }
        glog.Infof("Snapshot details: %#v", snapshot)
        snap.ID = snapshot.ID
        snap.Name = snapshot. Name
        snap.Status = snapshot.Status
        snap.SourceVolumeID = snapshot.VolumeID

	return snap, nil
}

func (snapshots *SnapshotsV2) deleteSnapshot(snapshotID string) error {
	err := snapshotsV2.Delete(snapshots.blockstorage, snapshotID).ExtractErr()
	if err != nil {
		glog.Errorf("Cannot delete snapshot %s: %v", snapshotID, err)
	}

	return err
}

// CreateSnapshot from the specified volume
func (os *OpenStack) CreateSnapshot(sourceVolumeID, name, description string, tags map[string]string) (string, error) {
	snapshots, err := os.snapshotService()
	if err != nil || snapshots == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return "", err
	}

	opts := SnapshotCreateOpts{
		VolumeID:    sourceVolumeID,
		Name:        name,
		Description: description,
	}
	if tags != nil {
		opts.Metadata = tags
	}

	snapshotID, err := snapshots.createSnapshot(opts)

	if err != nil {
		glog.Errorf("Failed to snapshot volume %s : %v", sourceVolumeID, err)
		return "", err
	}

	glog.Infof("Created snapshot %v from volume: %v", snapshotID, sourceVolumeID)
	return snapshotID, nil
}

// DeleteSnapshot deletes the specified snapshot
func (os *OpenStack) DeleteSnapshot(snapshotID string) error {
	snapshots, err := os.snapshotService()
	if err != nil || snapshots == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return err
	}

	err = snapshots.deleteSnapshot(snapshotID)
	if err != nil {
		glog.Errorf("Cannot delete snapshot %s: %v", snapshotID, err)
	}
	return nil
}

// FIXME(j-griffith): Name doesn't fit at all here, this is actually more like is `IsAvailable`
// DescribeSnapshot returns the status of the snapshot
func (os *OpenStack) DescribeSnapshot(snapshotID string) (isCompleted bool, err error) {
	ss, err := os.snapshotService()
	if err != nil || ss == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return false, err
	}

	snap, err := ss.getSnapshot(snapshotID)
	if err != nil {
		glog.Errorf("error requesting snapshot %s: %v", snapshotID, err)
	}

	if err != nil {
		return false, err
	}
	if snap.Status != "available" {
		return false, fmt.Errorf("current snapshot status is: %s", snap.Status)
	}
	return true, nil
}
