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

// Package reconciler implements interfaces that attempt to reconcile the
// desired state of the with the actual state of the world by triggering
// actions.
package reconciler

import (
	"time"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/cache"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/snapshotter"
)

// Reconciler runs a periodic loop to reconcile the desired state of the with
// the actual state of the world by triggering the volume snapshot operations.
type Reconciler interface {
	// Starts running the reconciliation loop which executes periodically, creates
	// and deletes VolumeSnapshotData for the user created and deleted VolumeSnapshot
	// objects and triggers the actual snapshot creation in the volume backends.
	Run(stopCh <-chan struct{})
}

type reconciler struct {
	loopPeriod                time.Duration
	syncDuration              time.Duration
	desiredStateOfWorld       cache.DesiredStateOfWorld
	actualStateOfWorld        cache.ActualStateOfWorld
	snapshotter               snapshotter.VolumeSnapshotter
	timeOfLastSync            time.Time
	disableReconciliationSync bool
}

// NewReconciler is the constructor of Reconciler
func NewReconciler(
	loopPeriod time.Duration,
	syncDuration time.Duration,
	disableReconciliationSync bool,
	desiredStateOfWorld cache.DesiredStateOfWorld,
	actualStateOfWorld cache.ActualStateOfWorld,
	snapshotter snapshotter.VolumeSnapshotter) Reconciler {
	return &reconciler{
		loopPeriod:                loopPeriod,
		syncDuration:              syncDuration,
		disableReconciliationSync: disableReconciliationSync,
		desiredStateOfWorld:       desiredStateOfWorld,
		actualStateOfWorld:        actualStateOfWorld,
		snapshotter:               snapshotter,
		timeOfLastSync:            time.Now(),
	}
}

func (rc *reconciler) Run(stopCh <-chan struct{}) {
	wait.Until(rc.reconciliationLoopFunc(), rc.loopPeriod, stopCh)
}

// reconciliationLoopFunc this can be disabled via cli option disableReconciliation.
// It periodically checks whether the VolumeSnapshots have corresponding SnapshotData
// and creates or deletes the snapshots when required.
func (rc *reconciler) reconciliationLoopFunc() func() {
	return func() {

		rc.reconcile()

		if rc.disableReconciliationSync {
			glog.V(5).Info("Skipping reconciling volume snapshots it is disabled via the command line.")
		} else if rc.syncDuration < time.Second {
			glog.V(5).Info("Skipping reconciling volume snapshots since it is set to less than one second via the command line.")
		} else if time.Since(rc.timeOfLastSync) > rc.syncDuration {
			glog.V(5).Info("Starting reconciling volume snapshots")
			rc.sync()
		}
	}
}

func (rc *reconciler) sync() {
	defer rc.updateSyncTime()
	rc.syncStates()
}

func (rc *reconciler) updateSyncTime() {
	rc.timeOfLastSync = time.Now()
}

func (rc *reconciler) syncStates() {
	//	volumesPerNode := rc.actualStateOfWorld.GetAttachedVolumesPerNode()
	//	rc.attacherDetacher.VerifyVolumesAreAttached(volumesPerNode, rc.actualStateOfWorld)
}

func (rc *reconciler) reconcile() {
	//glog.Infof("Volume snapshots are being reconciled")
	// Ensure the snapshots that should be deleted are deleted
	for name, snapshot := range rc.actualStateOfWorld.GetSnapshots() {
		if !rc.desiredStateOfWorld.SnapshotExists(name) {
			// Call snapshotter to start deleting the snapshot: it should
			// use the volume plugin to actually remove the on-disk snapshot.
			// It's likely that the operation exists already: it should be fired by the controller right
			// after the event has arrived.
			rc.snapshotter.DeleteVolumeSnapshot(snapshot)
		}
	}
	// Ensure the snapshots that should be created are created
	for name, snapshot := range rc.desiredStateOfWorld.GetSnapshots() {
		if !rc.actualStateOfWorld.SnapshotExists(name) {
			// Call snapshotter to start creating the snapshot: it should use the volume
			// plugin to create the on-disk snapshot, create the SnapshotData object for it
			// and update adn put the Snapshot object to the actualStateOfWorld once the operation finishes.
			// It's likely that the operation exists already: it should be fired by the controller right
			// after the event has arrived.
			rc.snapshotter.CreateVolumeSnapshot(snapshot)
		}
	}
}
