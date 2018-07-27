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

package controller

import (
	"time"

	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	kcontroller "k8s.io/kubernetes/pkg/controller"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/cache"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/reconciler"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/snapshotter"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"

	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/populator"
)

const (
	reconcilerLoopPeriod time.Duration = 100 * time.Millisecond

	// desiredStateOfWorldPopulatorLoopSleepPeriod is the amount of time the
	// DesiredStateOfWorldPopulator loop waits between successive executions
	desiredStateOfWorldPopulatorLoopSleepPeriod time.Duration = 1 * time.Minute

	// desiredStateOfWorldPopulatorListPodsRetryDuration is the amount of
	// time the DesiredStateOfWorldPopulator loop waits between list snapshots
	// calls.
	desiredStateOfWorldPopulatorListSnapshotsRetryDuration time.Duration = 3 * time.Minute
)

// SnapshotController is a controller that handles snapshot operations
type SnapshotController interface {
	Run(stopCh <-chan struct{})
}

type snapshotController struct {
	snapshotClient *rest.RESTClient
	snapshotScheme *runtime.Scheme

	// desiredStateOfWorld is a data structure containing the desired state of
	// the world according to this controller: i.e. what VolumeSnapshots need
	// the VolumeSnapshotData to be created, what VolumeSnapshotData and their
	// representing "on-disk" snapshots to be removed.
	desiredStateOfWorld cache.DesiredStateOfWorld

	// actualStateOfWorld is a data structure containing the actual state of
	// the world according to this controller: i.e. which VolumeSnapshots and
	// VolumeSnapshot data exist and to which PV/PVCs are associated.
	actualStateOfWorld cache.ActualStateOfWorld

	// reconciler is used to run an asynchronous periodic loop to create and delete
	// VolumeSnapshotData for the user created and deleted VolumeSnapshot objects and
	// trigger the actual snapshot creation in the volume backends.
	reconciler reconciler.Reconciler

	// Volume snapshotter is responsible for talking to the backend and creating, removing
	// or promoting the snapshots.
	snapshotter snapshotter.VolumeSnapshotter
	// recorder is used to record events in the API server
	recorder record.EventRecorder

	snapshotStore      kcache.Store
	snapshotController kcache.Controller

	// desiredStateOfWorldPopulator runs an asynchronous periodic loop to
	// populate the current snapshots using snapshotInformer.
	desiredStateOfWorldPopulator populator.DesiredStateOfWorldPopulator
}

// NewSnapshotController creates a new SnapshotController
func NewSnapshotController(client *rest.RESTClient,
	scheme *runtime.Scheme,
	clientset kubernetes.Interface,
	volumePlugins *map[string]volume.Plugin,
	syncDuration time.Duration) SnapshotController {

	sc := &snapshotController{
		snapshotClient: client,
		snapshotScheme: scheme,
	}

	// Watch snapshot objects
	source := kcache.NewListWatchFromClient(
		sc.snapshotClient,
		crdv1.VolumeSnapshotResourcePlural,
		apiv1.NamespaceAll,
		fields.Everything())

	sc.snapshotStore, sc.snapshotController = kcache.NewInformer(
		source,

		// The object type.
		&crdv1.VolumeSnapshot{},

		// resyncPeriod
		// Every resyncPeriod, all resources in the kcache will retrigger events.
		// Set to 0 to disable the resync.
		time.Minute*60,

		// Your custom resource event handlers.
		kcache.ResourceEventHandlerFuncs{
			AddFunc:    sc.onSnapshotAdd,
			UpdateFunc: sc.onSnapshotUpdate,
			DeleteFunc: sc.onSnapshotDelete,
		})

	//eventBroadcaster := record.NewBroadcaster()
	//eventBroadcaster.StartLogging(glog.Infof)
	//eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(client).Events("")})
	//	sc.recorder = eventBroadcaster.NewRecorder(api.Scheme, apiv1.EventSource{Component: "volume snapshotting"})

	sc.desiredStateOfWorld = cache.NewDesiredStateOfWorld()
	sc.actualStateOfWorld = cache.NewActualStateOfWorld()

	sc.snapshotter = snapshotter.NewVolumeSnapshotter(
		client,
		scheme,
		clientset,
		sc.actualStateOfWorld,
		volumePlugins)

	sc.reconciler = reconciler.NewReconciler(
		reconcilerLoopPeriod,
		syncDuration,
		false, /* disableReconciliationSync */
		sc.desiredStateOfWorld,
		sc.actualStateOfWorld,
		sc.snapshotter)

	sc.desiredStateOfWorldPopulator = populator.NewDesiredStateOfWorldPopulator(
		desiredStateOfWorldPopulatorLoopSleepPeriod,
		desiredStateOfWorldPopulatorListSnapshotsRetryDuration,
		sc.snapshotStore,
		sc.desiredStateOfWorld,
	)

	return sc
}

// Run starts an Snapshot resource controller
func (c *snapshotController) Run(ctx <-chan struct{}) {
	glog.Infof("Starting snapshot controller")

	go c.snapshotController.Run(ctx)

	if !kcontroller.WaitForCacheSync("snapshot-controller", ctx, c.snapshotController.HasSynced) {
		return
	}

	go c.reconciler.Run(ctx)
	go c.desiredStateOfWorldPopulator.Run(ctx)

}

func (c *snapshotController) onSnapshotAdd(obj interface{}) {
	// Add snapshot: Add snapshot to DesiredStateOfWorld, then ask snapshotter to create
	// the actual snapshot
	snapshotObj, ok := obj.(*crdv1.VolumeSnapshot)
	if !ok {
		glog.Warningf("expecting type VolumeSnapshot but received type %T", obj)
		return
	}
	snapshot := snapshotObj.DeepCopy()

	glog.Infof("[CONTROLLER] OnAdd %s, Snapshot %#v", snapshot.Metadata.SelfLink, snapshot)
	c.desiredStateOfWorld.AddSnapshot(snapshot)
}

func (c *snapshotController) onSnapshotUpdate(oldObj, newObj interface{}) {
	oldSnapshot := oldObj.(*crdv1.VolumeSnapshot)
	newSnapshot := newObj.(*crdv1.VolumeSnapshot)
	glog.Infof("[CONTROLLER] OnUpdate oldObj: %#v", oldSnapshot.Spec)
	glog.Infof("[CONTROLLER] OnUpdate newObj: %#v", newSnapshot.Spec)
	if oldSnapshot.Spec.SnapshotDataName != newSnapshot.Spec.SnapshotDataName {
		c.desiredStateOfWorld.AddSnapshot(newSnapshot)
	}
}

func (c *snapshotController) onSnapshotDelete(obj interface{}) {
	deletedSnapshot, ok := obj.(*crdv1.VolumeSnapshot)
	if !ok {
		// DeletedFinalStateUnkown is an expected data type here
		deletedState, isState := obj.(kcache.DeletedFinalStateUnknown)
		if !isState {
			glog.Errorf("Error: unkown type passed as snapshot for deletion: %T", obj)
			return
		}
		deletedSnapshot, ok = deletedState.Obj.(*crdv1.VolumeSnapshot)
		if !ok {
			glog.Errorf("Error: unkown data type in DeletedState: %T", deletedState.Obj)
			return
		}
	}
	// Delete snapshot: Remove the snapshot from DesiredStateOfWorld, then ask snapshotter to delete
	// the snapshot itself
	snapshot := deletedSnapshot.DeepCopy()
	glog.Infof("[CONTROLLER] OnDelete %s, snapshot name: %s/%s\n", snapshot.Metadata.SelfLink, snapshot.Metadata.Namespace, snapshot.Metadata.Name)
	c.desiredStateOfWorld.DeleteSnapshot(cache.MakeSnapshotName(snapshot))

}
