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
	"sync"

	"github.com/golang/glog"
	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
)

// ActualStateOfWorld defines a set of thread-safe operations supported on
// the snapshot controller's actual state of the world cache.
// This cache contains snapshots the snapshot controller believes are
// successfully created.
type ActualStateOfWorld interface {
	// Adds snapshot to the list of snapshots. No-op if the snapshot
	// is already in the list.
	AddSnapshot(*crdv1.VolumeSnapshot) error

	// Deletes the snapshot from the list of known snapshots. No-op if the snapshot
	// does not exist.
	DeleteSnapshot(snapshotName string) error

	// Return a copy of the known snapshots
	GetSnapshots() map[string]*crdv1.VolumeSnapshot

	// Get snapshot by its name
	GetSnapshot(snapshotName string) *crdv1.VolumeSnapshot

	// Check whether the specified snapshot exists
	SnapshotExists(snapshotName string) bool
}

type actualStateOfWorld struct {
	// List of snapshots that need to be created
	// it maps [snapshotName] VolumeSnapshot
	snapshots map[string]*crdv1.VolumeSnapshot
	sync.RWMutex
}

// NewActualStateOfWorld returns a new instance of ActualStateOfWorld.
func NewActualStateOfWorld() ActualStateOfWorld {
	m := make(map[string]*crdv1.VolumeSnapshot)
	return &actualStateOfWorld{
		snapshots: m,
	}
}

// Adds a snapshot to the list of snapshots to be created.
func (asw *actualStateOfWorld) AddSnapshot(snapshot *crdv1.VolumeSnapshot) error {
	asw.Lock()
	defer asw.Unlock()

	snapshotName := MakeSnapshotName(snapshot)
	glog.Infof("Adding new snapshot to actual state of world: %s", snapshotName)
	asw.snapshots[snapshotName] = snapshot
	return nil
}

// Removes the snapshot from the list of existing snapshots.
func (asw *actualStateOfWorld) DeleteSnapshot(snapshotName string) error {
	asw.Lock()
	defer asw.Unlock()

	glog.Infof("Deleting snapshot from actual state of world: %s", snapshotName)
	delete(asw.snapshots, snapshotName)
	return nil
}

// Returns a copy of the list of the snapshots known to the actual state of world.
func (asw *actualStateOfWorld) GetSnapshots() map[string]*crdv1.VolumeSnapshot {
	asw.RLock()
	defer asw.RUnlock()

	snapshots := make(map[string]*crdv1.VolumeSnapshot)

	for snapName, snapshot := range asw.snapshots {
		snapshots[snapName] = snapshot
	}

	return snapshots
}

// Get snapshot
func (asw *actualStateOfWorld) GetSnapshot(snapshotName string) *crdv1.VolumeSnapshot {
	asw.RLock()
	defer asw.RUnlock()
	snapshot, _ := asw.snapshots[snapshotName]

	return snapshot
}

// Checks for the existence of the snapshot
func (asw *actualStateOfWorld) SnapshotExists(snapshotName string) bool {
	asw.RLock()
	defer asw.RUnlock()
	_, snapshotExists := asw.snapshots[snapshotName]

	return snapshotExists
}
