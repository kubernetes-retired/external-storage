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
	ctrlsnap "github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/snapshotter"
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
	Description    string
	Metadata       map[string]string
}

// SnapshotCreateOpts are the valid create options for Cinder Snapshots
type SnapshotCreateOpts struct {
	VolumeID    string
	Name        string
	Description string
	Force       bool
	Metadata    map[string]string
}

// SnapshotListOpts are the valid list options for Cinder Snapshots
type SnapshotListOpts struct {
	Name     string
	Status   string
	VolumeID string
}

type snapshotService interface {
	createSnapshot(opts SnapshotCreateOpts) (string, string, error)
	deleteSnapshot(snapshotName string) error
	getSnapshot(snapshotID string) (Snapshot, error)
	listSnapshots(opts SnapshotListOpts) ([]Snapshot, error)
}

func (snapshots *SnapshotsV2) createSnapshot(opts SnapshotCreateOpts) (string, string, error) {

	createOpts := snapshotsV2.CreateOpts{
		VolumeID:    opts.VolumeID,
		Force:       false,
		Name:        opts.Name,
		Description: opts.Description,
		Metadata:    opts.Metadata,
	}

	snap, err := snapshotsV2.Create(snapshots.blockstorage, createOpts).Extract()
	if err != nil {
		return "", "", err
	}
	return snap.ID, snap.Status, nil
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
	snap.Name = snapshot.Name
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

func (snapshots *SnapshotsV2) listSnapshots(opts SnapshotListOpts) ([]Snapshot, error) {
	var snaplist []Snapshot

	listOpts := snapshotsV2.ListOpts{
		Name:     opts.Name,
		Status:   opts.Status,
		VolumeID: opts.VolumeID,
	}

	snapPages, err := snapshotsV2.List(snapshots.blockstorage, listOpts).AllPages()
	if err != nil {
		return snaplist, err
	}
	allSnaps, err := snapshotsV2.ExtractSnapshots(snapPages)
	if err != nil {
		return snaplist, err
	}

	for _, snapshot := range allSnaps {
		glog.Infof("Snapshot details: %#v", snapshot)
		var snap Snapshot
		snap.ID = snapshot.ID
		snap.Name = snapshot.Name
		snap.Status = snapshot.Status
		snap.SourceVolumeID = snapshot.VolumeID
		snap.Description = snapshot.Description
		snap.Metadata = snapshot.Metadata
		snaplist = append(snaplist, snap)
	}

	return snaplist, nil
}

// CreateSnapshot from the specified volume
func (os *OpenStack) CreateSnapshot(sourceVolumeID, name, description string, tags map[string]string) (string, string, error) {
	snapshots, err := os.snapshotService()
	if err != nil || snapshots == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return "", "", fmt.Errorf("Failed to create snapshot for volume %s: %v", sourceVolumeID, err)
	}

	opts := SnapshotCreateOpts{
		VolumeID:    sourceVolumeID,
		Name:        name,
		Description: description,
	}
	if tags != nil {
		opts.Metadata = tags
	}

	snapshotID, status, err := snapshots.createSnapshot(opts)

	if err != nil {
		glog.Errorf("Failed to snapshot volume %s : %v", sourceVolumeID, err)
		return "", "", err
	}

	glog.Infof("Created snapshot %v from volume: %v", snapshotID, sourceVolumeID)
	return snapshotID, status, nil
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

// DescribeSnapshot returns the status of the snapshot
// FIXME(j-griffith): Name doesn't fit at all here, this is actually more like is `IsAvailable`
func (os *OpenStack) DescribeSnapshot(snapshotID string) (status string, isCompleted bool, err error) {
	ss, err := os.snapshotService()
	if err != nil || ss == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return "", false, fmt.Errorf("Failed to describe snapshot %s: %v", snapshotID, err)
	}

	snap, err := ss.getSnapshot(snapshotID)
	if err != nil {
		glog.Errorf("error requesting snapshot %s: %v", snapshotID, err)
	}

	if err != nil {
		return "", false, err
	}
	if snap.Status != "available" {
		return snap.Status, false, nil
	}
	return snap.Status, true, nil
}

// FindSnapshot finds snapshot by metadata
func (os *OpenStack) FindSnapshot(tags map[string]string) ([]string, []string, error) {
	var snapshotIDs, statuses []string
	ss, err := os.snapshotService()
	if err != nil || ss == nil {
		glog.Errorf("Unable to initialize cinder client for region: %s", os.region)
		return snapshotIDs, statuses, fmt.Errorf("Failed to find snapshot by tags %v: %v", tags, err)
	}

	opts := SnapshotListOpts{}
	snapshots, err := ss.listSnapshots(opts)

	if err != nil {
		glog.Errorf("Failed to list snapshots. Error: %v", err)
		return snapshotIDs, statuses, err
	}
	glog.Infof("Listed [%v] snapshots.", len(snapshots))

	glog.Infof("Looking for matching tags [%#v] in snapshots.", tags)
	// Loop around to find the snapshot with the matching input metadata
	// NOTE(xyang): Metadata based filtering for snapshots is supported by Cinder volume API
	// microversion 3.21 and above. Currently the OpenStack Cloud Provider only supports V2.0.
	// Revisit this later when V3.0 is supported.
	for _, snapshot := range snapshots {
		glog.Infof("Looking for matching tags in snapshot [%#v].", snapshot)
		namespaceVal, ok := snapshot.Metadata[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotNamespaceTag]
		if ok {
			if tags[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotNamespaceTag] == namespaceVal {
				nameVal, ok := snapshot.Metadata[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotNameTag]
				if ok {
					if tags[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotNameTag] == nameVal {
						uidVal, ok := snapshot.Metadata[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotUIDTag]
						if ok {
							if tags[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotUIDTag] == uidVal {
								timeVal, ok := snapshot.Metadata[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotTimestampTag]
								if ok {
									if tags[ctrlsnap.CloudSnapshotCreatedForVolumeSnapshotTimestampTag] == timeVal {
										snapshotIDs = append(snapshotIDs, snapshot.ID)
										statuses = append(statuses, snapshot.Status)
										glog.Infof("Add snapshot [%#v].", snapshot)
									}
								}
							}
						}

					}
				}
			}
		}
	}

	return snapshotIDs, statuses, nil
}
