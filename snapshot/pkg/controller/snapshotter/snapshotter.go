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

package snapshotter

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubernetes/pkg/util/goroutinemap"
	"k8s.io/kubernetes/pkg/util/goroutinemap/exponentialbackoff"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/cache"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
)

const (
	defaultExponentialBackOffOnError = true

	// volumeSnapshot* is configuration of exponential backoff for
	// waiting for snapshot operation to complete. Starting with 10
	// seconds, multiplying by 1.2 with each step and taking 15 steps at maximum.
	// It will time out after 12.00 minutes.
	volumeSnapshotInitialDelay = 10 * time.Second
	volumeSnapshotFactor       = 1.2
	volumeSnapshotSteps        = 15
)

// VolumeSnapshotter does the "heavy lifting": it spawns goroutines that talk to the
// backend to actually perform the operations on the storage devices.
// It creates and deletes the snapshots and promotes snapshots to volumes (PV). The create
// and delete operations need to be idempotent and count with the fact the API object writes
type VolumeSnapshotter interface {
	CreateVolumeSnapshot(snapshot *crdv1.VolumeSnapshot)
	DeleteVolumeSnapshot(snapshot *crdv1.VolumeSnapshot)
	PromoteVolumeSnapshotToPV(snapshot *crdv1.VolumeSnapshot)
	UpdateVolumeSnapshot(snapshotName string, status *[]crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshot, error)
	UpdateVolumeSnapshotData(snapshotDataName string, status *[]crdv1.VolumeSnapshotDataCondition) error
}

type volumeSnapshotter struct {
	restClient         *rest.RESTClient
	coreClient         kubernetes.Interface
	scheme             *runtime.Scheme
	actualStateOfWorld cache.ActualStateOfWorld
	runningOperation   goroutinemap.GoRoutineMap
	volumePlugins      *map[string]volume.Plugin
}

const (
	snapshotOpCreatePrefix  string = "create"
	snapshotOpDeletePrefix  string = "delete"
	snapshotOpPromotePrefix string = "promote"
	// CloudSnapshotCreatedForVolumeSnapshotNamespaceTag is a name of a tag attached to a real snapshot in cloud
	// (e.g. AWS EBS or GCE PD) with namespace of a volumesnapshot used to create this snapshot.
	CloudSnapshotCreatedForVolumeSnapshotNamespaceTag = "kubernetes.io/created-for/snapshot/namespace"
	// CloudSnapshotCreatedForVolumeSnapshotNameTag is a name of a tag attached to a real snapshot in cloud
	// (e.g. AWS EBS or GCE PD) with name of a volumesnapshot used to create this snapshot.
	CloudSnapshotCreatedForVolumeSnapshotNameTag = "kubernetes.io/created-for/snapshot/name"
	// CloudSnapshotCreatedForVolumeSnapshotTimestampTag is a name of a tag attached to a real snapshot in cloud
	// (e.g. AWS EBS or GCE PD) with timestamp when the create snapshot request is issued.
	CloudSnapshotCreatedForVolumeSnapshotTimestampTag = "kubernetes.io/created-for/snapshot/timestamp"
	// Statuses of snapshot creation process
	statusReady   string = "ready"
	statusError   string = "error"
	statusPending string = "pending"
	statusNew     string = "new"
)

// NewVolumeSnapshotter create a new VolumeSnapshotter
func NewVolumeSnapshotter(
	restClient *rest.RESTClient,
	scheme *runtime.Scheme,
	clientset kubernetes.Interface,
	asw cache.ActualStateOfWorld,
	volumePlugins *map[string]volume.Plugin) VolumeSnapshotter {
	return &volumeSnapshotter{
		restClient:         restClient,
		coreClient:         clientset,
		scheme:             scheme,
		actualStateOfWorld: asw,
		runningOperation:   goroutinemap.NewGoRoutineMap(defaultExponentialBackOffOnError),
		volumePlugins:      volumePlugins,
	}
}

// TODO(xyang): Cache PV volume information into meta data to avoid query api server
// Helper function to get PV from VolumeSnapshot
func (vs *volumeSnapshotter) getPVFromVolumeSnapshot(snapshotName string, snapshot *crdv1.VolumeSnapshot) (*v1.PersistentVolume, error) {
	pvcName := snapshot.Spec.PersistentVolumeClaimName
	if pvcName == "" {
		return nil, fmt.Errorf("The PVC name is not specified in snapshot %s", snapshotName)
	}
	snapNameSpace, _, err := cache.GetNameAndNameSpaceFromSnapshotName(snapshotName)
	if err != nil {
		return nil, fmt.Errorf("Snapshot %s is malformed", snapshotName)
	}
	pvc, err := vs.coreClient.CoreV1().PersistentVolumeClaims(snapNameSpace).Get(pvcName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve PVC %s from the API server: %q", pvcName, err)
	}
	if pvc.Status.Phase != v1.ClaimBound {
		return nil, fmt.Errorf("The PVC %s not yet bound to a PV, will not attempt to take a snapshot yet", pvcName)
	}

	pvName := pvc.Spec.VolumeName
	pv, err := vs.coreClient.CoreV1().PersistentVolumes().Get(pvName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve PV %s from the API server: %q", pvName, err)
	}
	return pv, nil
}

// Helper function that looks up VolumeSnapshotData for a VolumeSnapshot named snapshotName
func (vs *volumeSnapshotter) getSnapshotDataFromSnapshotName(snapshotName string) *crdv1.VolumeSnapshotData {
	var snapshotDataList crdv1.VolumeSnapshotDataList
	var snapshotDataObj crdv1.VolumeSnapshotData
	var found bool

	err := vs.restClient.Get().
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
		Namespace(v1.NamespaceDefault).
		Do().Into(&snapshotDataList)
	if err != nil {
		glog.Errorf("Error retrieving the VolumeSnapshotData objects from API server: %v", err)
		return nil
	}
	if len(snapshotDataList.Items) == 0 {
		glog.Infof("No VolumeSnapshotData objects found on the API server")
		return nil
	}
	for _, snapData := range snapshotDataList.Items {
		if snapData.Spec.VolumeSnapshotRef != nil {
			name := snapData.Spec.VolumeSnapshotRef.Namespace + "/" + snapData.Spec.VolumeSnapshotRef.Name
			if name == snapshotName || snapData.Spec.VolumeSnapshotRef.Name == snapshotName {
				snapshotDataObj = snapData
				found = true
				break
			}
		}
	}
	if !found {
		glog.Errorf("Error: no VolumeSnapshotData for VolumeSnapshot %s found", snapshotName)
		return nil
	}

	return &snapshotDataObj
}

// Query status of the snapshot from plugin and update the status of VolumeSnapshot and VolumeSnapshotData
// if needed. Finish waiting when the snapshot becomes available/ready or error.
func (vs *volumeSnapshotter) waitForSnapshot(snapshotName string, snapshot *crdv1.VolumeSnapshot, snapshotDataObj *crdv1.VolumeSnapshotData) error {
	// Get a fresh VolumeSnapshot from API
	var snapshotObj crdv1.VolumeSnapshot
	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)
	if err != nil {
		return fmt.Errorf("Error getting snapshot object %s from API server: %v", snapshotName, err)
	}
	snapshotDataName := snapshotObj.Spec.SnapshotDataName
	glog.Infof("In waitForSnapshot: snapshot %s snapshot data %s", snapshotName, snapshotDataName)
	if snapshotDataObj == nil {
		return fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s: no VolumeSnapshotData", snapshotName)
	}

	spec := &snapshotDataObj.Spec
	volumeType := crdv1.GetSupportedVolumeFromSnapshotDataSpec(spec)
	if len(volumeType) == 0 {
		return fmt.Errorf("unsupported volume type found in snapshot %#v", spec)
	}
	plugin, ok := (*vs.volumePlugins)[volumeType]
	if !ok {
		return fmt.Errorf("%s is not supported volume for %#v", volumeType, spec)
	}

	var newSnapshot *crdv1.VolumeSnapshot
	var conditions *[]crdv1.VolumeSnapshotCondition
	var status, newstatus string = "", ""
	backoff := wait.Backoff{
		Duration: volumeSnapshotInitialDelay,
		Factor:   volumeSnapshotFactor,
		Steps:    volumeSnapshotSteps,
	}
	// Wait until the snapshot is successfully created by the plugin or an error occurs that
	// fails the snapshot creation.
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		conditions, _, err = plugin.DescribeSnapshot(snapshotDataObj)
		if err != nil {
			glog.Warningf("failed to get snapshot %v, err: %v", snapshotName, err)
			//continue waiting
			return false, nil
		}
		newstatus = vs.getSimplifiedSnapshotStatus(conditions)
		if newstatus == statusPending {
			if newstatus != status {
				status = newstatus
				glog.V(5).Infof("Snapshot %s creation is not complete yet. Status: [%#v] Retrying...", snapshotName, conditions)
				// UpdateVolmeSnapshot status
				newSnapshot, err = vs.UpdateVolumeSnapshot(snapshotName, conditions)
				if err != nil {
					glog.Errorf("Error updating volume snapshot %s: %v", snapshotName, err)
					return true, fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s: %v", snapshotName, err)
				}
			}
			// continue waiting
			return false, nil
		} else if newstatus == statusError {
			return true, fmt.Errorf("Status for snapshot %s is error", snapshotName)
		}
		glog.Infof("waitForSnapshot: Snapshot %s creation is complete: %#v", snapshotName, conditions)

		newSnapshot, err = vs.UpdateVolumeSnapshot(snapshotName, conditions)
		if err != nil {
			glog.Errorf("Error updating volume snapshot %s: %v", snapshotName, err)
			return true, fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s: %v", snapshotName, err)
		}

		ind := len(*conditions) - 1
		snapDataConditions := []crdv1.VolumeSnapshotDataCondition{
			{
				Type:               (crdv1.VolumeSnapshotDataConditionType)((*conditions)[ind].Type),
				Status:             (*conditions)[ind].Status,
				Message:            (*conditions)[ind].Message,
				LastTransitionTime: metav1.Now(),
			},
		}

		// Update VolumeSnapshotData status
		err = vs.UpdateVolumeSnapshotData(snapshotDataName, &snapDataConditions)
		if err != nil {
			return true, fmt.Errorf("Error update snapshotData object %s: %v", snapshotName, err)
		}

		if newstatus == statusReady {
			glog.Infof("waitForSnapshot: Snapshot %s created successfully. Adding it to Actual State of World.", snapshotName)
			vs.actualStateOfWorld.AddSnapshot(newSnapshot)
			// Break out of the for loop
			return true, nil
		} else if newstatus == statusError {
			return true, fmt.Errorf("Failed to create snapshot %s", snapshotName)
		}
		return false, nil
	})

	return err
}

// This is the function responsible for determining the correct volume plugin to use,
// asking it to make a snapshot and assigning it some name that it returns to the caller.
func (vs *volumeSnapshotter) takeSnapshot(pv *v1.PersistentVolume, tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	volumeType := crdv1.GetSupportedVolumeFromPVSpec(spec)
	if len(volumeType) == 0 {
		return nil, nil, fmt.Errorf("unsupported volume type found in PV %#v", spec)
	}
	plugin, ok := (*vs.volumePlugins)[volumeType]
	if !ok {
		return nil, nil, fmt.Errorf("%s is not supported volume for %#v", volumeType, spec)
	}

	snap, snapConditions, err := plugin.SnapshotCreate(pv, tags)
	if err != nil {
		glog.Warningf("failed to snapshot %#v, err: %v", spec, err)
	} else {
		glog.Infof("snapshot created: %v. Conditions: %#v", snap, snapConditions)
		return snap, snapConditions, nil
	}
	return nil, nil, nil
}

// This is the function responsible for determining the correct volume plugin to use,
// asking it to make a snapshot and assigning it some name that it returns to the caller.
func (vs *volumeSnapshotter) deleteSnapshot(spec *crdv1.VolumeSnapshotDataSpec) error {
	volumeType := crdv1.GetSupportedVolumeFromSnapshotDataSpec(spec)
	if len(volumeType) == 0 {
		return fmt.Errorf("unsupported volume type found in VolumeSnapshotData %#v", spec)
	}
	plugin, ok := (*vs.volumePlugins)[volumeType]
	if !ok {
		return fmt.Errorf("%s is not supported volume for %#v", volumeType, spec)
	}
	source := spec.VolumeSnapshotDataSource
	err := plugin.SnapshotDelete(&source, nil /* *v1.PersistentVolume */)
	if err != nil {
		return fmt.Errorf("failed to delete snapshot %#v, err: %v", source, err)
	}
	glog.Infof("snapshot %#v deleted", source)

	return nil
}

func (vs *volumeSnapshotter) getSimplifiedSnapshotStatus(conditions *[]crdv1.VolumeSnapshotCondition) string {
	if conditions == nil {
		glog.Errorf("Invalid input conditions for snapshot.")
		return statusError
	}
	index := len(*conditions) - 1
	if len(*conditions) > 0 &&
		((*conditions)[index].Type == crdv1.VolumeSnapshotConditionReady &&
			(*conditions)[index].Status == v1.ConditionTrue) {
		return statusReady
	} else if len(*conditions) > 0 &&
		(*conditions)[index].Type == crdv1.VolumeSnapshotConditionError {
		return statusError
	} else if len(*conditions) > 0 &&
		(*conditions)[index].Type == crdv1.VolumeSnapshotConditionPending &&
		((*conditions)[index].Status == v1.ConditionTrue ||
			(*conditions)[index].Status == v1.ConditionUnknown) {
		return statusPending
	}
	return statusNew
}

func (vs *volumeSnapshotter) findVolumeSnapshotMetadata(snapshot *crdv1.VolumeSnapshot) *map[string]string {
	var tags *map[string]string
	if snapshot.Metadata.Name != "" && snapshot.Metadata.Namespace != "" {
		if snapshot.Metadata.Labels != nil {
			timestamp, ok := snapshot.Metadata.Labels["Timestamp"]
			if ok {
				tags := make(map[string]string)
				tags[CloudSnapshotCreatedForVolumeSnapshotNamespaceTag] = snapshot.Metadata.Namespace
				tags[CloudSnapshotCreatedForVolumeSnapshotNameTag] = snapshot.Metadata.Name
				tags[CloudSnapshotCreatedForVolumeSnapshotTimestampTag] = timestamp
				glog.Infof("findVolumeSnapshotMetadata: returning tags [%#v]", tags)
			}
		}
	}
	return tags
}

func (vs *volumeSnapshotter) getPlugin(snapshotName string, snapshot *crdv1.VolumeSnapshot) (*volume.Plugin, error) {
	pv, err := vs.getPVFromVolumeSnapshot(snapshotName, snapshot)
	if err != nil {
		return nil, err
	}
	spec := &pv.Spec
	volumeType := crdv1.GetSupportedVolumeFromPVSpec(spec)
	if len(volumeType) == 0 {
		return nil, fmt.Errorf("Unsupported volume type found in PV %#v", spec)
	}
	plugin, ok := (*vs.volumePlugins)[volumeType]
	if !ok {
		return nil, fmt.Errorf("%s is not supported volume for %#v", volumeType, spec)
	}
	return &plugin, nil
}

func (vs *volumeSnapshotter) getSnapshotStatus(snapshot *crdv1.VolumeSnapshot) (string, *crdv1.VolumeSnapshotData, error) {
	var bCreateSnapData = false
	var snapshotName string = snapshot.Metadata.Name
	var snapshotDataObj *crdv1.VolumeSnapshotData
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var conditions *[]crdv1.VolumeSnapshotCondition
	var err error
	status := vs.getSimplifiedSnapshotStatus(&snapshot.Status.Conditions)
	if status == statusReady {
		return status, nil, nil
	} else if status == statusPending {
		// If we are here, takeSnapshot has already happened.
		// Check whether the VolumeSnapshotData object is already created
		snapshotDataObj = vs.getSnapshotDataFromSnapshotName(snapshotName)
		if snapshotDataObj == nil {
			bCreateSnapData = true
			// Find snapshot by existing tags, and create VolumeSnapshotData
		} else {
			// Bind VolumeSnapshotData to VolumeSnapshot if it has not happened yet
			if snapshot.Spec.SnapshotDataName != snapshotDataObj.Metadata.Name {
				glog.Infof("getSnapshotStatus: bind VolumeSnapshotData to VolumeSnapshot %s.", snapshotName)
				err = vs.bindVolumeSnapshotDataToVolumeSnapshot(snapshotName, snapshotDataObj.Metadata.Name)
				if err != nil {
					glog.Errorf("getSnapshotStatus: Error updating volume snapshot %s: %v", snapshotName, err)
					return statusError, nil, err
				}
			}
			return status, snapshotDataObj, nil
		}
	} else if status == statusError {
		return status, nil, fmt.Errorf("Failed to find snapshot %s", snapshotName)
	}
	bCreateSnapData = true
	// Find snapshot by existing tags, and create VolumeSnapshotData

	if bCreateSnapData {
		snapshotDataSource, conditions, err = vs.findSnapshot(snapshotName, snapshot)
		if err != nil {
			return statusNew, nil, nil
		}
		glog.Infof("getSnapshotStatus: create VolumeSnapshotData object for VolumeSnapshot %s.", snapshotName)
		snapshotDataObj, err := vs.createVolumeSnapshotData(snapshotName, snapshot, snapshotDataSource)
		if err != nil {
			return statusError, nil, err
		}
		if status != statusReady {
			_, err = vs.UpdateVolumeSnapshot(snapshotName, conditions)
			if err != nil {
				glog.Errorf("getSnapshotStatus: Error updating volume snapshot %s: %v", snapshotName, err)
				return statusError, nil, err
			}
		}
		glog.Infof("getSnapshotStatus: bind VolumeSnapshotData to VolumeSnapshot %s.", snapshotName)
		err = vs.bindVolumeSnapshotDataToVolumeSnapshot(snapshotName, snapshotDataObj.Metadata.Name)
		if err != nil {
			glog.Errorf("getSnapshotStatus: Error binding VolumeSnapshotData to VolumeSnapshot %s: %v", snapshotName, err)
			return statusError, nil, err
		}
		return statusPending, snapshotDataObj, nil
	}
	return statusError, nil, nil
}

// Below are the closures meant to build the functions for the GoRoutineMap operations.
// syncSnapshot is the main controller method to decide what to do to create a snapshot.
func (vs *volumeSnapshotter) syncSnapshot(snapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
	return func() error {
		status, snapshotDataObj, err := vs.getSnapshotStatus(snapshot)
		switch status {
		case statusReady:
			return nil
		case statusError:
			glog.Infof("syncSnapshot: Error creating snapshot %s.", snapshotName)
			return fmt.Errorf("Error creating snapshot %s", snapshotName)
		case statusPending:
			glog.Infof("syncSnapshot: Snapshot %s is Pending.", snapshotName)
			// Query the volume plugin for the status of the snapshot with snapshot id
			// from VolumeSnapshotData object.
			// Add snapshot to Actual State of World when snapshot is Ready.
			err = vs.waitForSnapshot(snapshotName, snapshot, snapshotDataObj)
			if err != nil {
				return fmt.Errorf("Failed to create snapshot %s", snapshotName)
			}
			glog.Infof("syncSnapshot: Snapshot %s created successfully.", snapshotName)
			return nil
		case statusNew:
			glog.Infof("syncSnapshot: Creating snapshot %s ...", snapshotName)
			ret := vs.createSnapshot(snapshotName, snapshot)
			return ret
		}
		return fmt.Errorf("Error occurred when creating snapshot %s", snapshotName)
	}
}

func (vs *volumeSnapshotter) findSnapshot(snapshotName string, snapshot *crdv1.VolumeSnapshot) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("findSnapshot: snapshot %s", snapshotName)
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var conditions *[]crdv1.VolumeSnapshotCondition
	tags := vs.findVolumeSnapshotMetadata(snapshot)
	if tags != nil {
		plugin, err := vs.getPlugin(snapshotName, snapshot)
		if plugin == nil {
			glog.Errorf("Failed to get volume plugin. %v", err)
			return nil, nil, fmt.Errorf("Failed to get volume plugin to create snapshot %s", snapshotName)
		}
		// Check whether the real snapshot is already created by the plugin
		glog.Infof("findSnapshot: find snapshot %s by tags %v.", snapshotName, tags)
		snapshotDataSource, conditions, err = (*plugin).FindSnapshot(tags)
		if err == nil {
			glog.Infof("findSnapshot: found snapshot %s.", snapshotName)
			return snapshotDataSource, conditions, nil
		}
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("No metadata found in snapshot %s", snapshotName)
}

func (vs *volumeSnapshotter) createSnapshot(snapshotName string, snapshot *crdv1.VolumeSnapshot) error {
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var snapStatus *[]crdv1.VolumeSnapshotCondition
	var err error
	var tags *map[string]string
	glog.Infof("createSnapshot: Create metadata for snapshot %s.", snapshotName)
	tags, err = vs.updateVolumeSnapshotMetadata(snapshot)
	if err != nil {
		return fmt.Errorf("Failed to update metadata for volume snapshot %s: %q", snapshotName, err)
	}

	glog.Infof("createSnapshot: Creating snapshot %s through the plugin ...", snapshotName)
	pv, err := vs.getPVFromVolumeSnapshot(snapshotName, snapshot)
	if err != nil {
		return err
	}
	snapshotDataSource, snapStatus, err = vs.takeSnapshot(pv, tags)
	if err != nil || snapshotDataSource == nil {
		return fmt.Errorf("Failed to take snapshot of the volume %s: %q", pv.Name, err)
	}

	glog.Infof("createSnapshot: Update status for VolumeSnapshot object %s.", snapshotName)
	_, err = vs.UpdateVolumeSnapshot(snapshotName, snapStatus)
	if err != nil {
		glog.Errorf("createSnapshot: Error updating volume snapshot %s: %v", snapshotName, err)
		return fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s", snapshotName)
	}

	glog.Infof("createSnapshot: create VolumeSnapshotData object for VolumeSnapshot %s.", snapshotName)
	snapshotDataObj, err := vs.createVolumeSnapshotData(snapshotName, snapshot, snapshotDataSource)
	if err != nil {
		return err
	}

	glog.Infof("createSnapshot: bind VolumeSnapshotData to VolumeSnapshot %s.", snapshotName)
	err = vs.bindVolumeSnapshotDataToVolumeSnapshot(snapshotName, snapshotDataObj.Metadata.Name)
	if err != nil {
		glog.Errorf("createSnapshot: Error binding VolumeSnapshotData to VolumeSnapshot %s: %v", snapshotName, err)
		return fmt.Errorf("Failed to bind VolumeSnapshotData to VolumeSnapshot %s", snapshotName)
	}

	// Waiting for snapshot to be ready
	err = vs.waitForSnapshot(snapshotName, snapshot, snapshotDataObj)
	if err != nil {
		return fmt.Errorf("Failed to create snapshot %s", snapshotName)
	}
	glog.Infof("createSnapshot: Snapshot %s created successfully.", snapshotName)
	return nil
}

func (vs *volumeSnapshotter) createVolumeSnapshotData(snapshotName string, snapshot *crdv1.VolumeSnapshot, snapshotDataSource *crdv1.VolumeSnapshotDataSource) (*crdv1.VolumeSnapshotData, error) {
	var snapshotObj crdv1.VolumeSnapshot
	// Need to get a fresh copy of the VolumeSnapshot with the updated status
	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)

	conditions := snapshotObj.Status.Conditions
	if len(conditions) == 0 {
		glog.Infof("createVolumeSnapshotData: Failed to create VolumeSnapshotDate for snapshot %s. No status info from the VolumeSnapshot object.", snapshotName)
		return nil, fmt.Errorf("Failed to create VolumeSnapshotData for snapshot %s", snapshotName)
	}
	ind := len(conditions) - 1
	glog.Infof("createVolumeSnapshotData: Snapshot %s. Conditions: %#v", snapshotName, conditions)
	readyCondition := crdv1.VolumeSnapshotDataCondition{
		Type:    (crdv1.VolumeSnapshotDataConditionType)(conditions[ind].Type),
		Status:  conditions[ind].Status,
		Message: conditions[ind].Message,
	}
	snapName := "k8s-volume-snapshot-" + string(uuid.NewUUID())

	pv, err := vs.getPVFromVolumeSnapshot(snapshotName, snapshot)
	if err != nil {
		return nil, err
	}
	pvName := pv.Name

	snapshotData := &crdv1.VolumeSnapshotData{
		Metadata: metav1.ObjectMeta{
			Name: snapName,
		},
		Spec: crdv1.VolumeSnapshotDataSpec{
			VolumeSnapshotRef: &v1.ObjectReference{
				Kind: "VolumeSnapshot",
				Name: snapshotName,
			},
			PersistentVolumeRef: &v1.ObjectReference{
				Kind: "PersistentVolume",
				Name: pvName,
			},
			VolumeSnapshotDataSource: *snapshotDataSource,
		},
		Status: crdv1.VolumeSnapshotDataStatus{
			Conditions: []crdv1.VolumeSnapshotDataCondition{
				readyCondition,
			},
		},
	}
	backoff := wait.Backoff{
		Duration: volumeSnapshotInitialDelay,
		Factor:   volumeSnapshotFactor,
		Steps:    volumeSnapshotSteps,
	}
	var result crdv1.VolumeSnapshotData
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		err = vs.restClient.Post().
			Resource(crdv1.VolumeSnapshotDataResourcePlural).
			Namespace(v1.NamespaceDefault).
			Body(snapshotData).
			Do().Into(&result)
		if err != nil {
			// Re-Try it as errors writing to the API server are common
			return false, err
		}
		return true, nil
	})

	if err != nil {
		glog.Errorf("createVolumeSnapshotData: Error creating the VolumeSnapshotData %s: %v", snapshotName, err)
		return nil, fmt.Errorf("Failed to create the VolumeSnapshotData %s for snapshot %s", snapName, snapshotName)
	}
	return &result, nil
}

func (vs *volumeSnapshotter) getSnapshotDeleteFunc(snapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
	// Delete a snapshot
	// 1. Find the SnapshotData corresponding to Snapshot
	//   1a: Not found => finish (it's been deleted already)
	// 2. Ask the backend to remove the snapshot device
	// 3. Delete the SnapshotData object
	// 4. Remove the Snapshot from ActualStateOfWorld
	// 5. Finish
	return func() error {
		// TODO: get VolumeSnapshotDataSource from associated VolumeSnapshotData
		// then call volume delete snapshot method to delete the ot
		snapshotDataObj := vs.getSnapshotDataFromSnapshotName(snapshotName)
		if snapshotDataObj == nil {
			return fmt.Errorf("Error getting VolumeSnapshotData for VolumeSnapshot %s", snapshotName)
		}

		err := vs.deleteSnapshot(&snapshotDataObj.Spec)
		if err != nil {
			return fmt.Errorf("Failed to delete snapshot %s: %q", snapshotName, err)
		}

		snapshotDataName := snapshotDataObj.Metadata.Name
		var result metav1.Status
		err = vs.restClient.Delete().
			Name(snapshotDataName).
			Resource(crdv1.VolumeSnapshotDataResourcePlural).
			Namespace(v1.NamespaceDefault).
			Do().Into(&result)
		if err != nil {
			return fmt.Errorf("Failed to delete VolumeSnapshotData %s from API server: %q", snapshotDataName, err)
		}

		vs.actualStateOfWorld.DeleteSnapshot(snapshotName)

		return nil
	}
}

func (vs *volumeSnapshotter) getSnapshotPromoteFunc(snapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
	// Promote snapshot to a PVC
	// 1. We have a PVC referencing a Snapshot object
	// 2. Find the SnapshotData corresponding to tha Snapshot
	// 3. Ask the backend to give us a device (PV) made from the snapshot device
	// 4. Bind it to the PVC
	// 5. Finish
	return func() error { return nil }
}

func (vs *volumeSnapshotter) CreateVolumeSnapshot(snapshot *crdv1.VolumeSnapshot) {
	snapshotName := cache.MakeSnapshotName(snapshot.Metadata.Namespace, snapshot.Metadata.Name)
	operationName := snapshotOpCreatePrefix + snapshotName + snapshot.Spec.PersistentVolumeClaimName
	//glog.Infof("Snapshotter is about to create volume snapshot operation named %s, spec %#v", operationName, snapshot.Spec)

	err := vs.runningOperation.Run(operationName, vs.syncSnapshot(snapshotName, snapshot))

	if err != nil {
		switch {
		case goroutinemap.IsAlreadyExists(err):
			glog.V(4).Infof("operation %q is already running, skipping", operationName)
		case exponentialbackoff.IsExponentialBackoff(err):
			glog.V(4).Infof("operation %q postponed due to exponential backoff", operationName)
		default:
			glog.Errorf("Failed to schedule the operation %q: %v", operationName, err)
		}
	}
}

func (vs *volumeSnapshotter) DeleteVolumeSnapshot(snapshot *crdv1.VolumeSnapshot) {
	snapshotName := cache.MakeSnapshotName(snapshot.Metadata.Namespace, snapshot.Metadata.Name)
	operationName := snapshotOpDeletePrefix + snapshotName + snapshot.Spec.PersistentVolumeClaimName
	glog.Infof("Snapshotter is about to create volume snapshot operation named %s", operationName)

	err := vs.runningOperation.Run(operationName, vs.getSnapshotDeleteFunc(snapshotName, snapshot))

	if err != nil {
		switch {
		case goroutinemap.IsAlreadyExists(err):
			glog.V(4).Infof("operation %q is already running, skipping", operationName)
		case exponentialbackoff.IsExponentialBackoff(err):
			glog.V(4).Infof("operation %q postponed due to exponential backoff", operationName)
		default:
			glog.Errorf("Failed to schedule the operation %q: %v", operationName, err)
		}
	}
}

func (vs *volumeSnapshotter) PromoteVolumeSnapshotToPV(snapshot *crdv1.VolumeSnapshot) {
	snapshotName := cache.MakeSnapshotName(snapshot.Metadata.Namespace, snapshot.Metadata.Name)
	operationName := snapshotOpPromotePrefix + snapshotName + snapshot.Spec.PersistentVolumeClaimName
	glog.Infof("Snapshotter is about to create volume snapshot operation named %s", operationName)

	err := vs.runningOperation.Run(operationName, vs.getSnapshotPromoteFunc(snapshotName, snapshot))

	if err != nil {
		switch {
		case goroutinemap.IsAlreadyExists(err):
			glog.V(4).Infof("operation %q is already running, skipping", operationName)
		case exponentialbackoff.IsExponentialBackoff(err):
			glog.V(4).Infof("operation %q postponed due to exponential backoff", operationName)
		default:
			glog.Errorf("Failed to schedule the operation %q: %v", operationName, err)
		}
	}
}

func (vs *volumeSnapshotter) updateVolumeSnapshotMetadata(snapshot *crdv1.VolumeSnapshot) (*map[string]string, error) {
	glog.Infof("In updateVolumeSnapshotMetadata")
	var snapshotObj crdv1.VolumeSnapshot
	// Need to get a fresh copy of the VolumeSnapshot from the API server
	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)

	// Copy the snapshot object before updating it
	objCopy, err := vs.scheme.DeepCopy(&snapshotObj)
	if err != nil {
		return nil, fmt.Errorf("Error copying snapshot object %s object: %v", snapshot.Metadata.Name, err)
	}
	snapshotCopy, ok := objCopy.(*crdv1.VolumeSnapshot)
	if !ok {
		return nil, fmt.Errorf("Error: expecting type VolumeSnapshot but received type %T", objCopy)
	}

	tags := make(map[string]string)
	tags["Timestamp"] = fmt.Sprintf("%d", time.Now().UnixNano())
	snapshotCopy.Metadata.Labels = tags
	glog.Infof("updateVolumeSnapshotMetadata: Metadata Name: %s Metadata Namespace: %s Setting tags in Metadata Labels: %#v.", snapshotCopy.Metadata.Name, snapshotCopy.Metadata.Namespace, snapshotCopy.Metadata.Labels)

	var result crdv1.VolumeSnapshot
	err = vs.restClient.Put().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshotCopy.Metadata.Namespace).
		Body(snapshotCopy).
		Do().Into(&result)
	if err != nil {
		return nil, fmt.Errorf("Error updating snapshot object %s on the API server: %v", snapshot.Metadata.Name, err)
	}

	cloudTags := make(map[string]string)
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotNamespaceTag] = result.Metadata.Namespace
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotNameTag] = result.Metadata.Name
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotTimestampTag] = result.Metadata.Labels["Timestamp"]

	glog.Infof("updateVolumeSnapshotMetadata: returning cloudTags [%#v]", cloudTags)
	return &cloudTags, nil
}

func (vs *volumeSnapshotter) UpdateVolumeSnapshot(snapshotName string, status *[]crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshot, error) {
	var snapshotObj crdv1.VolumeSnapshot

	glog.Infof("In UpdateVolumeSnapshot")
	// Get a fresh copy of the VolumeSnapshot from the API server
	snapNameSpace, snapName, err := cache.GetNameAndNameSpaceFromSnapshotName(snapshotName)
	if err != nil {
		return nil, fmt.Errorf("Error getting namespace and name from VolumeSnapshot name %s: %v", snapshotName, err)
	}
	glog.Infof("UpdateVolumeSnapshot: Namespace %s Name %s", snapNameSpace, snapName)
	err = vs.restClient.Get().
		Name(snapName).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapNameSpace).
		Do().Into(&snapshotObj)

	objCopy, err := vs.scheme.DeepCopy(&snapshotObj)
	if err != nil {
		return nil, fmt.Errorf("Error copying snapshot object %s object from API server: %v", snapshotName, err)
	}
	snapshotCopy, ok := objCopy.(*crdv1.VolumeSnapshot)
	if !ok {
		return nil, fmt.Errorf("Error: expecting type VolumeSnapshot but received type %T", objCopy)
	}

	snapshotDataObj := vs.getSnapshotDataFromSnapshotName(snapshotName)
	if snapshotDataObj == nil {
		glog.Infof("UpdateVolumeSnapshot: VolumeSnapshotData not created for snapshot %s yet.", snapName)
	} else {
		glog.Infof("UpdateVolumeSnapshot: Setting VolumeSnapshotData name %s in VolumeSnapshot object %s", snapshotDataObj.Metadata.Name, snapName)
		snapshotCopy.Spec.SnapshotDataName = snapshotDataObj.Metadata.Name
	}

	if status != nil && len(*status) > 0 {
		glog.Infof("UpdateVolumeSnapshot: Setting status in VolumeSnapshot object.")
		// Add the new condition to existing ones if it has a different type or
		// update an existing condition of the same type.
		ind := len(*status) - 1
		ind2 := len(snapshotCopy.Status.Conditions)
		if ind2 < 1 || snapshotCopy.Status.Conditions[ind2-1].Type != (*status)[ind].Type {
			snapshotCopy.Status.Conditions = append(snapshotCopy.Status.Conditions, (*status)[ind])
		} else if snapshotCopy.Status.Conditions[ind2-1].Type == (*status)[ind].Type {
			snapshotCopy.Status.Conditions[ind2-1] = (*status)[ind]
		}
	}
	glog.Infof("Updating VolumeSnapshot object [%#v]", snapshotCopy)
	// TODO: Make diff of the two objects and then use restClient.Patch to update it
	var result crdv1.VolumeSnapshot
	err = vs.restClient.Put().
		Name(snapName).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapNameSpace).
		Body(snapshotCopy).
		Do().Into(&result)
	if err != nil {
		return nil, fmt.Errorf("Error updating snapshot object %s on the API server: %v", snapshotName, err)
	}

	return &result, nil
}

func (vs *volumeSnapshotter) bindVolumeSnapshotDataToVolumeSnapshot(snapshotName string, snapshotDataName string) error {
	var snapshotObj crdv1.VolumeSnapshot

	glog.Infof("In bindVolumeSnapshotDataToVolumeSnapshot")
	// Get a fresh copy of the VolumeSnapshot from the API server
	snapNameSpace, snapName, err := cache.GetNameAndNameSpaceFromSnapshotName(snapshotName)
	if err != nil {
		return fmt.Errorf("Error getting namespace and name from VolumeSnapshot name %s: %v", snapshotName, err)
	}
	glog.Infof("bindVolumeSnapshotDataToVolumeSnapshot: Namespace %s Name %s", snapNameSpace, snapName)
	err = vs.restClient.Get().
		Name(snapName).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapNameSpace).
		Do().Into(&snapshotObj)

	objCopy, err := vs.scheme.DeepCopy(&snapshotObj)
	if err != nil {
		return fmt.Errorf("Error copying snapshot object %s object from API server: %v", snapshotName, err)
	}
	snapshotCopy, ok := objCopy.(*crdv1.VolumeSnapshot)
	if !ok {
		return fmt.Errorf("Error: expecting type VolumeSnapshot but received type %T", objCopy)
	}

	snapshotCopy.Spec.SnapshotDataName = snapshotDataName
	glog.Infof("bindVolumeSnapshotDataToVolumeSnapshot: Updating VolumeSnapshot object [%#v]", snapshotCopy)
	// TODO: Make diff of the two objects and then use restClient.Patch to update it
	var result crdv1.VolumeSnapshot
	err = vs.restClient.Put().
		Name(snapName).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapNameSpace).
		Body(snapshotCopy).
		Do().Into(&result)
	if err != nil {
		return fmt.Errorf("Error updating snapshot object %s on the API server: %v", snapshotName, err)
	}

	return nil
}

func (vs *volumeSnapshotter) UpdateVolumeSnapshotData(snapshotDataName string, status *[]crdv1.VolumeSnapshotDataCondition) error {
	var snapshotDataObj crdv1.VolumeSnapshotData

	glog.Infof("In UpdateVolumeSnapshotData %s", snapshotDataName)
	err := vs.restClient.Get().
		Name(snapshotDataName).
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
		Namespace(v1.NamespaceDefault).
		Do().Into(&snapshotDataObj)

	objCopy, err := vs.scheme.DeepCopy(&snapshotDataObj)
	if err != nil {
		return fmt.Errorf("Error copying snapshot data object %s object from API server: %v", snapshotDataName, err)
	}
	snapshotDataCopy, ok := objCopy.(*crdv1.VolumeSnapshotData)
	if !ok {
		return fmt.Errorf("Error: expecting type VolumeSnapshotData but received type %T", objCopy)
	}

	// Add the new condition to existing ones if it has a different type or
	// update an existing condition of the same type.
	ind := len(*status) - 1
	ind2 := len(snapshotDataCopy.Status.Conditions)
	if ind2 < 1 || snapshotDataCopy.Status.Conditions[ind2-1].Type != (*status)[ind].Type {
		snapshotDataCopy.Status.Conditions = append(snapshotDataCopy.Status.Conditions, (*status)[ind])
	} else if snapshotDataCopy.Status.Conditions[ind2-1].Type == (*status)[ind].Type {
		snapshotDataCopy.Status.Conditions[ind2-1] = (*status)[ind]
	}
	glog.Infof("Updating VolumeSnapshotData object. Conditions: [%v]", snapshotDataCopy.Status.Conditions)
	// TODO: Make diff of the two objects and then use restClient.Patch to update it
	var result crdv1.VolumeSnapshotData
	err = vs.restClient.Put().
		Name(snapshotDataName).
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
		Namespace(v1.NamespaceDefault).
		Body(snapshotDataCopy).
		Do().Into(&result)
	if err != nil {
		return fmt.Errorf("Error updating snapshotdata object %s on the API server: %v", snapshotDataName, err)
	}
	return nil
}
