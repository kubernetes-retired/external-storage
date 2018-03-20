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

// Package populator implements interfaces that monitor and keep the states of the
// desired_state_of_word in sync with the "ground truth" from informer.
package populator

import (
	"github.com/golang/glog"
	listers "github.com/kubernetes-incubator/external-storage/snapshot/pkg/client/listers/volumesnapshot/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/cache"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"time"
)

// DesiredStateOfWorldPopulator periodically verifies that the snapshot in the
// desired state of the world still exist, if not, it removes them.
// It also loops through the list of snapshots in the actual state of the world
// and ensures that each one exists in the desired state of the world cache
type DesiredStateOfWorldPopulator interface {
	Run(stopCh <-chan struct{})
}

// NewDesiredStateOfWorldPopulator returns a new instance of DesiredStateOfWorldPopulator.
// loopSleepDuration - the amount of time the populator loop sleeps between
//     successive executions
// desiredStateOfWorld - the cache to populate
func NewDesiredStateOfWorldPopulator(
	loopSleepDuration time.Duration,
	listSnapshotsRetryDuration time.Duration,
	snapshotStore listers.VolumeSnapshotLister,
	desiredStateOfWorld cache.DesiredStateOfWorld) DesiredStateOfWorldPopulator {
	return &desiredStateOfWorldPopulator{
		loopSleepDuration:          loopSleepDuration,
		listSnapshotsRetryDuration: listSnapshotsRetryDuration,
		desiredStateOfWorld:        desiredStateOfWorld,
		snapshotStore:              snapshotStore,
	}
}

type desiredStateOfWorldPopulator struct {
	loopSleepDuration          time.Duration
	listSnapshotsRetryDuration time.Duration
	timeOfLastListSnapshots    time.Time
	desiredStateOfWorld        cache.DesiredStateOfWorld
	snapshotStore              listers.VolumeSnapshotLister
}

func (dswp *desiredStateOfWorldPopulator) Run(stopCh <-chan struct{}) {
	wait.Until(dswp.populatorLoopFunc(), dswp.loopSleepDuration, stopCh)
}

func (dswp *desiredStateOfWorldPopulator) populatorLoopFunc() func() {
	return func() {
		dswp.findAndRemoveDeletedSnapshots()

		// findAndAddActiveSnapshots is called periodically, independently of the main
		// populator loop.
		if time.Since(dswp.timeOfLastListSnapshots) < dswp.listSnapshotsRetryDuration {
			glog.V(5).Infof(
				"Skipping findAndAddActiveSnapshots(). Not permitted until %v (listSnapshotsRetryDuration %v).",
				dswp.timeOfLastListSnapshots.Add(dswp.listSnapshotsRetryDuration),
				dswp.listSnapshotsRetryDuration)

			return
		}
		dswp.findAndAddActiveSnapshots()
	}
}

// Iterate through all pods in desired state of world, and remove if they no
// longer exist in the informer
func (dswp *desiredStateOfWorldPopulator) findAndRemoveDeletedSnapshots() {
	for snapshotUID, snapshot := range dswp.desiredStateOfWorld.GetSnapshots() {
		_, err := dswp.snapshotStore.VolumeSnapshots(snapshot.ObjectMeta.Namespace).Get(snapshot.ObjectMeta.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				glog.V(1).Infof("Removing snapshot %s from dsw because it does not exist in snapshot informer.", snapshotUID)
				dswp.desiredStateOfWorld.DeleteSnapshot(cache.MakeSnapshotName(snapshot.ObjectMeta.Namespace, snapshot.ObjectMeta.Name))
			} else {
				glog.Errorf("get snapshot %s failed: %v", snapshotUID, err)
				continue
			}
		}
	}
}

func (dswp *desiredStateOfWorldPopulator) findAndAddActiveSnapshots() {
	snapshotList, err := dswp.snapshotStore.List(labels.Everything())
	if err != nil {
		glog.Errorf("get snapshot list failed: %v", err)
	}
	for _, snapshot := range snapshotList {
		snapshotName := cache.MakeSnapshotName(snapshot.ObjectMeta.Namespace, snapshot.ObjectMeta.Name)
		if !dswp.desiredStateOfWorld.SnapshotExists(snapshotName) {
			glog.V(1).Infof("Adding snapshot %s to dsw because it exists in snapshot informer.", snapshotName)
			dswp.desiredStateOfWorld.AddSnapshot(snapshot)
		}
	}
}
