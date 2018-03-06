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

/*
Package cache implements data structures used by the snapshot controller
to keep track of volume snapshots.
*/
package cache

import (
	"fmt"
	"sync"

	"github.com/golang/glog"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
)

// DesiredStateOfWorld defines a set of thread-safe operations supported on
// the snapshot controller's desired state of the world cache. This cache
// contains all the snapshots that should be created by the snapshot controller.
type DesiredStateOfWorld interface {
	// Adds snapshot to the list of snapshots. No-op if the snapshot
	// is already in the list.
	AddSnapshot(*crdv1.VolumeSnapshot) error

	// Deletes the snapshot from the list of known snapshots. No-op if the snapshot
	// does not exist.
	DeleteSnapshot(snapshotName string) error

	// Return a copy of the known snapshots
	GetSnapshots() map[string]*crdv1.VolumeSnapshot

	// Check whether the specified snapshot exists
	SnapshotExists(snapshotName string) bool
}

type desiredStateOfWorld struct {
	// List of snapshots that exist in the desired state of world
	// it maps [snapshotName] VolumeSnapshot
	snapshots map[string]*crdv1.VolumeSnapshot
	sync.RWMutex
}

// NewDesiredStateOfWorld returns a new instance of DesiredStateOfWorld.
func NewDesiredStateOfWorld() DesiredStateOfWorld {
	m := make(map[string]*crdv1.VolumeSnapshot)
	return &desiredStateOfWorld{
		snapshots: m,
	}
}

// Adds a snapshot to the list of snapshots to be created
func (dsw *desiredStateOfWorld) AddSnapshot(snapshot *crdv1.VolumeSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("nil snapshot spec")
	}

	dsw.Lock()
	defer dsw.Unlock()

	snapshotName := MakeSnapshotName(snapshot)
	glog.Infof("Adding new snapshot to desired state of world: %s", snapshotName)
	dsw.snapshots[snapshotName] = snapshot
	return nil
}

// Removes the snapshot from the list of existing snapshots
func (dsw *desiredStateOfWorld) DeleteSnapshot(snapshotName string) error {
	dsw.Lock()
	defer dsw.Unlock()

	glog.Infof("Deleting snapshot from desired state of world: %s", snapshotName)

	delete(dsw.snapshots, snapshotName)
	return nil
}

// Returns a copy of the list of the snapshots known to the actual state of world.
func (dsw *desiredStateOfWorld) GetSnapshots() map[string]*crdv1.VolumeSnapshot {
	dsw.RLock()
	defer dsw.RUnlock()

	snapshots := make(map[string]*crdv1.VolumeSnapshot)

	for snapName, snapshot := range dsw.snapshots {
		snapshots[snapName] = snapshot
	}

	return snapshots
}

// Checks for the existence of the snapshot
func (dsw *desiredStateOfWorld) SnapshotExists(snapshotName string) bool {
	dsw.RLock()
	defer dsw.RUnlock()
	_, snapshotExists := dsw.snapshots[snapshotName]

	return snapshotExists
}
