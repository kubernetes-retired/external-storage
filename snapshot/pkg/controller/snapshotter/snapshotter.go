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

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/cache"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubernetes/pkg/util/goroutinemap"
	"k8s.io/kubernetes/pkg/util/goroutinemap/exponentialbackoff"
)

const (
	snapshotMetadataTimeStamp        = "SnapshotMetadata-Timestamp"
	snapshotMetadataPVName           = "SnapshotMetadata-PVName"
	snapshotDataNamePrefix           = "k8s-volume-snapshot"
	pvNameLabel                      = "pvName"
	defaultExponentialBackOffOnError = true

	// volumeSnapshot* is configuration of exponential backoff for
	// waiting for snapshot operation to complete. Starting with 2
	// seconds, multiplying by 1.5 with each step and taking 20 steps at maximum.
	// It will time out after 20 steps (6650 seconds).
	volumeSnapshotInitialDelay = 2 * time.Second
	volumeSnapshotFactor       = 1.5
	volumeSnapshotSteps        = 20
)

// VolumeSnapshotter does the "heavy lifting": it spawns goroutines that talk to the
// backend to actually perform the operations on the storage devices.
// It creates and deletes the snapshots and promotes snapshots to volumes (PV). The create
// and delete operations need to be idempotent and count with the fact the API object writes
type VolumeSnapshotter interface {
	CreateVolumeSnapshot(snapshot *crdv1.VolumeSnapshot)
	DeleteVolumeSnapshot(snapshot *crdv1.VolumeSnapshot)
	PromoteVolumeSnapshotToPV(snapshot *crdv1.VolumeSnapshot)
	//UpdateVolumeSnapshot(snapshotName string, status *[]crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshot, error)
	//UpdateVolumeSnapshotData(snapshotDataName string, status *[]crdv1.VolumeSnapshotDataCondition) error
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
	// CloudSnapshotCreatedForVolumeSnapshotUIDTag is a name of a tag attached to a real snapshot in cloud
	// (e.g. AWS EBS or GCE PD) with uid of a volumesnapshot used to create this snapshot.
	CloudSnapshotCreatedForVolumeSnapshotUIDTag = "kubernetes.io/created-for/snapshot/uid"
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

// Helper function to get PV from VolumeSnapshot
func (vs *volumeSnapshotter) getPVFromVolumeSnapshot(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) (*v1.PersistentVolume, error) {
	pvcName := snapshot.Spec.PersistentVolumeClaimName
	if pvcName == "" {
		return nil, fmt.Errorf("The PVC name is not specified in snapshot %s", uniqueSnapshotName)
	}

	pvc, err := vs.coreClient.CoreV1().PersistentVolumeClaims(snapshot.Metadata.Namespace).Get(pvcName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve PVC %s from the API server: %q", pvcName, err)
	}
	if pvc.Status.Phase != v1.ClaimBound {
		return nil, fmt.Errorf("The PVC %s not yet bound to a PV, will not attempt to take a snapshot yet", pvcName)
	}

	pvName := pvc.Spec.VolumeName
	pv, err := vs.getPVFromName(pvName)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve PV %s from the API server: %q", pvName, err)
	}
	return pv, nil
}

// Helper function to get PV from PV name
func (vs *volumeSnapshotter) getPVFromName(pvName string) (*v1.PersistentVolume, error) {
	return vs.coreClient.CoreV1().PersistentVolumes().Get(pvName, metav1.GetOptions{})
}

// TODO: cache the VolumeSnapshotData list since this is only needed when controller restarts, checks
// whether there is existing VolumeSnapshotData refers to the snapshot already.
// Helper function that looks up VolumeSnapshotData for a VolumeSnapshot named snapshotName
func (vs *volumeSnapshotter) getSnapshotDataFromSnapshotName(uniqueSnapshotName string) *crdv1.VolumeSnapshotData {
	var snapshotDataList crdv1.VolumeSnapshotDataList
	var snapshotDataObj crdv1.VolumeSnapshotData
	var found bool

	err := vs.restClient.Get().
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
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
			if name == uniqueSnapshotName || snapData.Spec.VolumeSnapshotRef.Name == uniqueSnapshotName {
				snapshotDataObj = snapData
				found = true
				break
			}
		}
	}
	if !found {
		glog.V(4).Infof("Error: no VolumeSnapshotData for VolumeSnapshot %s found", uniqueSnapshotName)
		return nil
	}

	return &snapshotDataObj
}

// Helper function that looks up VolumeSnapshotData from a VolumeSnapshot
func (vs *volumeSnapshotter) getSnapshotDataFromSnapshot(snapshot *crdv1.VolumeSnapshot) (*crdv1.VolumeSnapshotData, error) {
	var snapshotDataObj crdv1.VolumeSnapshotData
	snapshotDataName := snapshot.Spec.SnapshotDataName
	if snapshotDataName == "" {
		return nil, fmt.Errorf("Could not find snapshot data object: SnapshotDataName in snapshot spec is empty")
	}
	err := vs.restClient.Get().
		Name(snapshotDataName).
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
		Do().Into(&snapshotDataObj)
	if err != nil {
		glog.Errorf("Error retrieving the VolumeSnapshotData objects from API server: %v", err)
		return nil, fmt.Errorf("Could not get snapshot data object %s: %v", snapshotDataName, err)
	}
	return &snapshotDataObj, nil
}

// Query status of the snapshot from plugin and update the status of VolumeSnapshot and VolumeSnapshotData
// if needed. Finish waiting when the snapshot becomes available/ready or error.
func (vs *volumeSnapshotter) waitForSnapshot(uniqueSnapshotName string, snapshotObj *crdv1.VolumeSnapshot, snapshotDataObj *crdv1.VolumeSnapshotData) error {
	glog.Infof("In waitForSnapshot: snapshot %s snapshot data %s", uniqueSnapshotName, snapshotObj.Spec.SnapshotDataName)
	if snapshotDataObj == nil {
		return fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s: no VolumeSnapshotData", uniqueSnapshotName)
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

	backoff := wait.Backoff{
		Duration: volumeSnapshotInitialDelay,
		Factor:   volumeSnapshotFactor,
		Steps:    volumeSnapshotSteps,
	}
	// Wait until the snapshot is successfully created by the plugin or an error occurs that
	// fails the snapshot creation.
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		conditions, _, err := plugin.DescribeSnapshot(snapshotDataObj)
		if err != nil {
			glog.Warningf("failed to get snapshot %v, err: %v", uniqueSnapshotName, err)
			//continue waiting
			return false, nil
		}

		newstatus := vs.getSimplifiedSnapshotStatus(*conditions)
		condition := *conditions
		lastCondition := condition[len(condition)-1]
		newSnapshot, err := vs.UpdateVolumeSnapshotStatus(snapshotObj, &lastCondition)
		if err != nil {
			glog.Errorf("Error updating volume snapshot %s: %v", uniqueSnapshotName, err)
		}

		if newstatus == statusReady {
			glog.Infof("waitForSnapshot: Snapshot %s created successfully. Adding it to Actual State of World.", uniqueSnapshotName)
			vs.actualStateOfWorld.AddSnapshot(newSnapshot)
			// Break out of the for loop
			return true, nil
		} else if newstatus == statusError {
			glog.Errorf("waitForSnapshot: Snapshot %s returns error", uniqueSnapshotName)
			return true, fmt.Errorf("Failed to create snapshot %s", uniqueSnapshotName)
		}
		return false, nil
	})

	return err
}

// This is the function responsible for determining the correct volume plugin to use,
// asking it to make a snapshot and assigning it some name that it returns to the caller.
func (vs *volumeSnapshotter) takeSnapshot(
	snapshot *crdv1.VolumeSnapshot,
	pv *v1.PersistentVolume,
	tags *map[string]string,
) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	spec := &pv.Spec
	volumeType := crdv1.GetSupportedVolumeFromPVSpec(spec)
	if len(volumeType) == 0 {
		return nil, nil, fmt.Errorf("unsupported volume type found in PV %#v", spec)
	}
	plugin, ok := (*vs.volumePlugins)[volumeType]
	if !ok {
		return nil, nil, fmt.Errorf("%s is not supported volume for %#v", volumeType, spec)
	}

	snapDataSource, snapConditions, err := plugin.SnapshotCreate(snapshot, pv, tags)
	if err != nil {
		glog.Warningf("failed to snapshot %#v, err: %v", spec, err)
	} else {
		glog.Infof("snapshot created: %v. Conditions: %#v", snapDataSource, snapConditions)
		return snapDataSource, snapConditions, nil
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
	pv, err := vs.getPVFromName(spec.PersistentVolumeRef.Name)
	if err != nil {
		glog.Warningf("failed to retrieve PV %s from the API server: %q", spec.PersistentVolumeRef.Name, err)
	}
	err = plugin.SnapshotDelete(&source, pv)
	if err != nil {
		return fmt.Errorf("failed to delete snapshot %#v, err: %v", source, err)
	}
	glog.Infof("snapshot %#v deleted", source)

	return nil
}

func (vs *volumeSnapshotter) getSimplifiedSnapshotStatus(conditions []crdv1.VolumeSnapshotCondition) string {
	if conditions == nil {
		glog.Errorf("No conditions for this snapshot yet.")
		return statusNew
	}
	if len(conditions) == 0 {
		glog.Errorf("Empty condition.")
		return statusNew
	}
	//index := len(conditions) - 1
	lastCondition := conditions[len(conditions)-1]
	if lastCondition.Type == crdv1.VolumeSnapshotConditionReady && lastCondition.Status == v1.ConditionTrue {
		return statusReady
	} else if lastCondition.Type == crdv1.VolumeSnapshotConditionError {
		return statusError
	} else if lastCondition.Type == crdv1.VolumeSnapshotConditionPending &&
		(lastCondition.Status == v1.ConditionTrue || lastCondition.Status == v1.ConditionUnknown) {
		return statusPending
	}
	return statusNew
}

func (vs *volumeSnapshotter) findVolumeSnapshotMetadata(snapshot *crdv1.VolumeSnapshot) *map[string]string {
	var tags map[string]string
	if snapshot.Metadata.Name != "" && snapshot.Metadata.Namespace != "" && snapshot.Metadata.UID != "" {
		if snapshot.Metadata.Labels != nil {
			timestamp, ok := snapshot.Metadata.Labels[snapshotMetadataTimeStamp]
			if ok {
				tags = make(map[string]string)
				tags[CloudSnapshotCreatedForVolumeSnapshotNamespaceTag] = snapshot.Metadata.Namespace
				tags[CloudSnapshotCreatedForVolumeSnapshotNameTag] = snapshot.Metadata.Name
				tags[CloudSnapshotCreatedForVolumeSnapshotUIDTag] = fmt.Sprintf("%v", snapshot.Metadata.UID)
				tags[CloudSnapshotCreatedForVolumeSnapshotTimestampTag] = timestamp
				glog.Infof("findVolumeSnapshotMetadata: returning tags [%#v]", tags)
			}
		}
	}
	return &tags
}

func (vs *volumeSnapshotter) getPlugin(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) (*volume.Plugin, error) {
	pv, err := vs.getPVFromVolumeSnapshot(uniqueSnapshotName, snapshot)
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

// Exame the given snapshot in detail and then return the status
func (vs *volumeSnapshotter) updateSnapshotIfExists(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) (string, *crdv1.VolumeSnapshot, error) {
	snapshotName := snapshot.Metadata.Name
	var snapshotDataObj *crdv1.VolumeSnapshotData
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var conditions *[]crdv1.VolumeSnapshotCondition
	var err error

	tags := vs.findVolumeSnapshotMetadata(snapshot)
	// If there is no tag returned, snapshotting is not triggered yet, return new state
	if tags == nil {
		glog.Infof("No tag can be found in snapshot metadata %s", uniqueSnapshotName)
		return statusNew, snapshot, nil
	}
	// Check whether snapshotData object is already created or not. If yes, snapshot is already
	// triggered through cloud provider, bind it and return pending state
	if snapshotDataObj = vs.getSnapshotDataFromSnapshotName(uniqueSnapshotName); snapshotDataObj != nil {
		glog.Infof("Find snapshot data object %s from snapshot %s", snapshotDataObj.Metadata.Name, uniqueSnapshotName)
		snapshotObj, err := vs.bindandUpdateVolumeSnapshot(snapshot, snapshotDataObj.Metadata.Name, nil)
		if err != nil {
			return statusError, snapshot, err
		}
		return statusPending, snapshotObj, nil
	}
	// Find snapshot through cloud provider by existing tags, and create VolumeSnapshotData if such snapshot is found
	snapshotDataSource, conditions, err = vs.findSnapshotByTags(snapshotName, snapshot)
	if err != nil {
		return statusNew, snapshot, nil
	}
	// Snapshot is found. Create VolumeSnapshotData, bind VolumeSnapshotData to VolumeSnapshot, and update VolumeSnapshot status
	glog.Infof("updateSnapshotIfExists: create VolumeSnapshotData object for VolumeSnapshot %s.", uniqueSnapshotName)
	pvName, ok := snapshot.Metadata.Labels[pvNameLabel]
	if !ok {
		return statusError, snapshot, fmt.Errorf("Could not find pv name from snapshot, this should not happen.")
	}
	snapshotDataObj, err = vs.createVolumeSnapshotData(snapshotName, pvName, snapshotDataSource, conditions)
	if err != nil {
		return statusError, snapshot, err
	}
	glog.Infof("updateSnapshotIfExists: update VolumeSnapshot status and bind VolumeSnapshotData to VolumeSnapshot %s.", uniqueSnapshotName)
	snapshotObj, err := vs.bindandUpdateVolumeSnapshot(snapshot, snapshotDataObj.Metadata.Name, conditions)
	if err != nil {
		return statusError, nil, err
	}
	return statusPending, snapshotObj, nil
}

// Below are the closures meant to build the functions for the GoRoutineMap operations.
// syncSnapshot is the main controller method to decide what to do to create a snapshot.
func (vs *volumeSnapshotter) syncSnapshot(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
	return func() error {
		snapshotObj := snapshot
		status := vs.getSimplifiedSnapshotStatus(snapshot.Status.Conditions)
		var err error
		// When the condition is new, it is still possible that snapshot is already triggered but has not yet updated the condition.
		// Check the metadata and avaiable VolumeSnapshotData objects and update the snapshot accordingly
		if status == statusNew {
			status, snapshotObj, err = vs.updateSnapshotIfExists(uniqueSnapshotName, snapshot)
			if err != nil {
				glog.Errorf("updateSnapshotIfExists has error %v", err)
			}
		}
		switch status {
		case statusReady:
			glog.Infof("Snapshot %s created successfully. Adding it to Actual State of World.", uniqueSnapshotName)
			vs.actualStateOfWorld.AddSnapshot(snapshot)
			return nil
		case statusError:
			glog.Infof("syncSnapshot: Error creating snapshot %s.", uniqueSnapshotName)
			return fmt.Errorf("Error creating snapshot %s", uniqueSnapshotName)
		case statusPending:
			glog.V(4).Infof("syncSnapshot: Snapshot %s is Pending.", uniqueSnapshotName)
			// Query the volume plugin for the status of the snapshot with snapshot id
			// from VolumeSnapshotData object.
			snapshotDataObj, err := vs.getSnapshotDataFromSnapshot(snapshotObj)
			if err != nil {
				return fmt.Errorf("Failed to find snapshot %v", err)
			}
			err = vs.waitForSnapshot(uniqueSnapshotName, snapshotObj, snapshotDataObj)
			if err != nil {
				return fmt.Errorf("Failed to check snapshot state %s with error %v", uniqueSnapshotName, err)
			}
			glog.Infof("syncSnapshot: Snapshot %s created successfully.", uniqueSnapshotName)
			return nil
		case statusNew:
			glog.Infof("syncSnapshot: Creating snapshot %s ...", uniqueSnapshotName)
			err = vs.createSnapshot(uniqueSnapshotName, snapshotObj)
			return err
		}
		return fmt.Errorf("Error occurred when creating snapshot %s, unknown status %s", uniqueSnapshotName, status)
	}
}

func (vs *volumeSnapshotter) findSnapshotByTags(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error) {
	glog.Infof("findSnapshot: snapshot %s", uniqueSnapshotName)
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var conditions *[]crdv1.VolumeSnapshotCondition
	tags := vs.findVolumeSnapshotMetadata(snapshot)
	if tags != nil {
		plugin, err := vs.getPlugin(uniqueSnapshotName, snapshot)
		if err != nil {
			glog.Errorf("Failed to get volume plugin. %v", err)
			return nil, nil, fmt.Errorf("Failed to get volume plugin to create snapshot %s", uniqueSnapshotName)
		}
		// Check whether the real snapshot is already created by the plugin
		glog.Infof("findSnapshot: find snapshot %s by tags %v.", uniqueSnapshotName, tags)
		snapshotDataSource, conditions, err = (*plugin).FindSnapshot(tags)
		if err == nil {
			glog.Infof("findSnapshot: found snapshot %s.", uniqueSnapshotName)
			return snapshotDataSource, conditions, nil
		}
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("No metadata found in snapshot %s", uniqueSnapshotName)
}

// The function goes through the whole snapshot creation process.
// 1. Update VolumeSnapshot metadata to include the snapshotted PV name, timestamp and snapshot uid, also generate tag for cloud provider
// 2. Trigger the snapshot through cloud provider and attach the tag to the snapshot.
// 3. Create the VolumeSnapshotData object with the snapshot id information returned from step 2.
// 4. Bind the VolumeSnapshot and VolumeSnapshotData object
// 5. Query the snapshot status through cloud provider and update the status until snapshot is ready or fails.
func (vs *volumeSnapshotter) createSnapshot(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) error {
	var snapshotDataSource *crdv1.VolumeSnapshotDataSource
	var snapStatus *[]crdv1.VolumeSnapshotCondition
	var err error
	var tags *map[string]string
	glog.Infof("createSnapshot: Creating snapshot %s through the plugin ...", uniqueSnapshotName)
	pv, err := vs.getPVFromVolumeSnapshot(uniqueSnapshotName, snapshot)
	if err != nil {
		return err
	}

	glog.Infof("createSnapshot: Creating metadata for snapshot %s.", uniqueSnapshotName)
	tags, err = vs.updateVolumeSnapshotMetadata(snapshot, pv.Name)
	if err != nil {
		return fmt.Errorf("Failed to update metadata for volume snapshot %s: %q", uniqueSnapshotName, err)
	}

	snapshotDataSource, snapStatus, err = vs.takeSnapshot(snapshot, pv, tags)
	if err != nil || snapshotDataSource == nil {
		return fmt.Errorf("Failed to take snapshot of the volume %s: %q", pv.Name, err)
	}

	glog.Infof("createSnapshot: create VolumeSnapshotData object for VolumeSnapshot %s.", uniqueSnapshotName)
	snapshotDataObj, err := vs.createVolumeSnapshotData(uniqueSnapshotName, pv.Name, snapshotDataSource, snapStatus)
	if err != nil {
		return err
	}

	glog.Infof("createSnapshot: Update VolumeSnapshot status and bind VolumeSnapshotData to VolumeSnapshot %s.", uniqueSnapshotName)
	snapshotObj, err := vs.bindandUpdateVolumeSnapshot(snapshot, snapshotDataObj.Metadata.Name, snapStatus)
	if err != nil {
		glog.Errorf("createSnapshot: Error updating volume snapshot %s: %v", uniqueSnapshotName, err)
		return fmt.Errorf("Failed to update VolumeSnapshot for snapshot %s", uniqueSnapshotName)
	}

	// Waiting for snapshot to be ready
	err = vs.waitForSnapshot(uniqueSnapshotName, snapshotObj, snapshotDataObj)
	if err != nil {
		return fmt.Errorf("Failed to create snapshot %s with error %v", uniqueSnapshotName, err)
	}
	glog.Infof("createSnapshot: Snapshot %s created successfully.", uniqueSnapshotName)
	return nil
}

func (vs *volumeSnapshotter) createVolumeSnapshotData(uniqueSnapshotName, pvName string,
	snapshotDataSource *crdv1.VolumeSnapshotDataSource, snapStatus *[]crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshotData, error) {

	glog.Infof("createVolumeSnapshotData: Snapshot %s. Conditions: %#v", uniqueSnapshotName, snapStatus)
	var lastCondition crdv1.VolumeSnapshotDataCondition
	if snapStatus != nil && len(*snapStatus) > 0 {
		conditions := *snapStatus
		ind := len(conditions) - 1
		lastCondition = crdv1.VolumeSnapshotDataCondition{
			Type:               (crdv1.VolumeSnapshotDataConditionType)(conditions[ind].Type),
			Status:             conditions[ind].Status,
			Message:            conditions[ind].Message,
			LastTransitionTime: metav1.Now(),
		}
	}
	// Generate snapshotData name with the UID of snapshot object
	snapDataName := fmt.Sprintf("%s-%s", snapshotDataNamePrefix, uuid.NewUUID())
	snapshotData := &crdv1.VolumeSnapshotData{
		Metadata: metav1.ObjectMeta{
			Name: snapDataName,
		},
		Spec: crdv1.VolumeSnapshotDataSpec{
			VolumeSnapshotRef: &v1.ObjectReference{
				Kind: "VolumeSnapshot",
				Name: uniqueSnapshotName,
			},
			PersistentVolumeRef: &v1.ObjectReference{
				Kind: "PersistentVolume",
				Name: pvName,
			},
			VolumeSnapshotDataSource: *snapshotDataSource,
		},
		Status: crdv1.VolumeSnapshotDataStatus{
			Conditions: []crdv1.VolumeSnapshotDataCondition{
				lastCondition,
			},
		},
	}
	// TODO: Do we need to try to update VolumeSnapshotData object multiple times until it succeed?
	// For all other updates, we only try it once.
	backoff := wait.Backoff{
		Duration: volumeSnapshotInitialDelay,
		Factor:   volumeSnapshotFactor,
		Steps:    volumeSnapshotSteps,
	}
	var result crdv1.VolumeSnapshotData
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := vs.restClient.Post().
			Resource(crdv1.VolumeSnapshotDataResourcePlural).
			Body(snapshotData).
			Do().Into(&result)
		if err != nil {
			// Re-Try it as errors writing to the API server are common
			return false, err
		}
		return true, nil
	})

	if err != nil {
		glog.Errorf("createVolumeSnapshotData: Error creating the VolumeSnapshotData %s: %v", uniqueSnapshotName, err)
		return nil, fmt.Errorf("Failed to create the VolumeSnapshotData %s for snapshot %s", snapDataName, uniqueSnapshotName)
	}
	return &result, nil
}

func (vs *volumeSnapshotter) getSnapshotDeleteFunc(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
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
		snapshotDataObj, err := vs.getSnapshotDataFromSnapshot(snapshot)
		if err != nil {
			return fmt.Errorf("Error getting VolumeSnapshotData for VolumeSnapshot %s with error %v", uniqueSnapshotName, err)
		}

		err = vs.deleteSnapshot(&snapshotDataObj.Spec)
		if err != nil {
			return fmt.Errorf("Failed to delete snapshot %s: %q", uniqueSnapshotName, err)
		}

		snapshotDataName := snapshotDataObj.Metadata.Name
		var result metav1.Status
		err = vs.restClient.Delete().
			Name(snapshotDataName).
			Resource(crdv1.VolumeSnapshotDataResourcePlural).
			Do().Into(&result)
		if err != nil {
			return fmt.Errorf("Failed to delete VolumeSnapshotData %s from API server: %q", snapshotDataName, err)
		}

		vs.actualStateOfWorld.DeleteSnapshot(uniqueSnapshotName)

		return nil
	}
}

func (vs *volumeSnapshotter) getSnapshotPromoteFunc(uniqueSnapshotName string, snapshot *crdv1.VolumeSnapshot) func() error {
	// Promote snapshot to a PVC
	// 1. We have a PVC referencing a Snapshot object
	// 2. Find the SnapshotData corresponding to tha Snapshot
	// 3. Ask the backend to give us a device (PV) made from the snapshot device
	// 4. Bind it to the PVC
	// 5. Finish
	return func() error { return nil }
}

func (vs *volumeSnapshotter) CreateVolumeSnapshot(snapshot *crdv1.VolumeSnapshot) {
	snapshotName := cache.MakeSnapshotName(snapshot)
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
	snapshotName := cache.MakeSnapshotName(snapshot)
	operationName := snapshotOpDeletePrefix + snapshotName + snapshot.Spec.PersistentVolumeClaimName
	glog.V(4).Infof("Snapshotter is about to delete volume snapshot operation named %s", operationName)

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
	snapshotName := cache.MakeSnapshotName(snapshot)
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

// Update VolumeSnapshot object with current timestamp and associated PersistentVolume name in object's metadata
func (vs *volumeSnapshotter) updateVolumeSnapshotMetadata(snapshot *crdv1.VolumeSnapshot, pvName string) (*map[string]string, error) {
	glog.Infof("In updateVolumeSnapshotMetadata")
	var snapshotObj crdv1.VolumeSnapshot
	// Need to get a fresh copy of the VolumeSnapshot from the API server
	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)
	if err != nil {
		return nil, fmt.Errorf("Error retrieving VolumeSnapshot %s from API server: %v", snapshot.Metadata.Name, err)
	}

	// Copy the snapshot object before updating it
	snapshotCopy := snapshotObj.DeepCopy()

	if snapshotCopy.Metadata.Labels == nil {
		snapshotCopy.Metadata.Labels = make(map[string]string)
	}
	snapshotCopy.Metadata.Labels[snapshotMetadataTimeStamp] = fmt.Sprintf("%d", time.Now().UnixNano())
	snapshotCopy.Metadata.Labels[snapshotMetadataPVName] = pvName
	glog.Infof("updateVolumeSnapshotMetadata: Metadata UID: %s Metadata Name: %s Metadata Namespace: %s Setting tags in Metadata Labels: %#v.",
		snapshotCopy.Metadata.UID, snapshotCopy.Metadata.Name, snapshotCopy.Metadata.Namespace, snapshotCopy.Metadata.Labels)

	// TODO: Use Patch instead of Put to update the object?
	var result crdv1.VolumeSnapshot
	err = vs.restClient.Put().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Body(snapshotCopy).
		Do().Into(&result)
	if err != nil {
		return nil, fmt.Errorf("Error updating snapshot object %s/%s on the API server: %v", snapshot.Metadata.Namespace, snapshot.Metadata.Name, err)
	}

	cloudTags := make(map[string]string)
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotNamespaceTag] = result.Metadata.Namespace
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotNameTag] = result.Metadata.Name
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotUIDTag] = fmt.Sprintf("%v", result.Metadata.UID)
	cloudTags[CloudSnapshotCreatedForVolumeSnapshotTimestampTag] = result.Metadata.Labels[snapshotMetadataTimeStamp]

	glog.Infof("updateVolumeSnapshotMetadata: returning cloudTags [%#v]", cloudTags)
	return &cloudTags, nil
}

// Propagates the VolumeSnapshot condition to VolumeSnapshotData
func (vs *volumeSnapshotter) propagateVolumeSnapshotCondition(snapshotDataName string, condition *crdv1.VolumeSnapshotCondition) error {
	var snapshotDataObj crdv1.VolumeSnapshotData
	err := vs.restClient.Get().
		Name(snapshotDataName).
		Resource(crdv1.VolumeSnapshotDataResourcePlural).
		Do().Into(&snapshotDataObj)
	if err != nil {
		return err
	}

	newCondition := &crdv1.VolumeSnapshotDataCondition{
		Type:               (crdv1.VolumeSnapshotDataConditionType)(condition.Type),
		Status:             condition.Status,
		Message:            condition.Message,
		LastTransitionTime: condition.LastTransitionTime,
	}
	oldStatus := snapshotDataObj.Status.DeepCopy()

	status := snapshotDataObj.Status
	isEqual := false
	if oldStatus.Conditions == nil || len(oldStatus.Conditions) == 0 || newCondition.Type != oldStatus.Conditions[len(oldStatus.Conditions)-1].Type {
		status.Conditions = append(status.Conditions, *newCondition)
	} else {
		oldCondition := oldStatus.Conditions[len(oldStatus.Conditions)-1]
		if newCondition.Status == oldCondition.Status {
			newCondition.LastTransitionTime = oldCondition.LastTransitionTime
		}
		status.Conditions[len(status.Conditions)-1] = *newCondition
		isEqual = newCondition.Type == oldCondition.Type &&
			newCondition.Status == oldCondition.Status &&
			newCondition.Reason == oldCondition.Reason &&
			newCondition.Message == oldCondition.Message &&
			newCondition.LastTransitionTime.Equal(&oldCondition.LastTransitionTime)
	}
	if !isEqual {
		var newSnapshotDataObj crdv1.VolumeSnapshotData
		snapshotDataObj.Status = status
		if snapshotDataObj.Status.CreationTimestamp.IsZero() && newCondition.Type == crdv1.VolumeSnapshotDataConditionReady {
			snapshotDataObj.Status.CreationTimestamp = newCondition.LastTransitionTime
		}
		err = vs.restClient.Put().
			Name(snapshotDataName).
			Resource(crdv1.VolumeSnapshotDataResourcePlural).
			Body(&snapshotDataObj).
			Do().Into(&newSnapshotDataObj)
		if err != nil {
			return err
		}
		glog.Infof("VolumeSnapshot status propagated to VolumeSnapshotData")
		return nil
	}

	return nil
}

// Update VolumeSnapshot status if the condition is changed.
func (vs *volumeSnapshotter) UpdateVolumeSnapshotStatus(snapshot *crdv1.VolumeSnapshot, condition *crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshot, error) {
	var snapshotObj crdv1.VolumeSnapshot

	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)
	if err != nil {
		return nil, err
	}
	oldStatus := snapshotObj.Status.DeepCopy()

	status := snapshotObj.Status
	isEqual := false
	if oldStatus.Conditions == nil || len(oldStatus.Conditions) == 0 || condition.Type != oldStatus.Conditions[len(oldStatus.Conditions)-1].Type {
		status.Conditions = append(status.Conditions, *condition)
	} else {
		oldCondition := oldStatus.Conditions[len(oldStatus.Conditions)-1]
		if condition.Status == oldCondition.Status {
			condition.LastTransitionTime = oldCondition.LastTransitionTime
		}
		status.Conditions[len(status.Conditions)-1] = *condition
		isEqual = condition.Type == oldCondition.Type &&
			condition.Status == oldCondition.Status &&
			condition.Reason == oldCondition.Reason &&
			condition.Message == oldCondition.Message &&
			condition.LastTransitionTime.Equal(&oldCondition.LastTransitionTime)
	}

	if !isEqual {
		var newSnapshotObj crdv1.VolumeSnapshot
		snapshotObj.Status = status
		err = vs.restClient.Put().
			Name(snapshot.Metadata.Name).
			Resource(crdv1.VolumeSnapshotResourcePlural).
			Namespace(snapshot.Metadata.Namespace).
			Body(&snapshotObj).
			Do().Into(&newSnapshotObj)
		if err != nil {
			return nil, err
		}
		glog.Infof("UpdateVolumeSnapshotStatus finishes %+v", newSnapshotObj)
		err = vs.propagateVolumeSnapshotCondition(snapshotObj.Spec.SnapshotDataName, &snapshotObj.Status.Conditions[len(snapshotObj.Status.Conditions)-1])
		if err != nil {
			return nil, err
		}
		return &newSnapshotObj, nil
	}

	return snapshot, nil
}

// Bind the VolumeSnapshot and VolumeSnapshotData and udpate the status
func (vs *volumeSnapshotter) bindandUpdateVolumeSnapshot(snapshot *crdv1.VolumeSnapshot, snapshotDataName string, status *[]crdv1.VolumeSnapshotCondition) (*crdv1.VolumeSnapshot, error) {
	var snapshotObj crdv1.VolumeSnapshot

	glog.Infof("In bindVolumeSnapshotDataToVolumeSnapshot")
	// Get a fresh copy of the VolumeSnapshot from the API server
	glog.Infof("bindVolumeSnapshotDataToVolumeSnapshot: Namespace %s Name %s", snapshot.Metadata.Namespace, snapshot.Metadata.Name)
	err := vs.restClient.Get().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Do().Into(&snapshotObj)

	uniqueSnapshotName := cache.MakeSnapshotName(snapshot)
	// TODO: Is copy needed here?
	snapshotCopy := snapshotObj.DeepCopy()

	snapshotCopy.Spec.SnapshotDataName = snapshotDataName
	if status != nil {
		snapshotCopy.Status.Conditions = *status
	}
	glog.Infof("bindVolumeSnapshotDataToVolumeSnapshot: Updating VolumeSnapshot object [%#v]", snapshotCopy)
	// TODO: Make diff of the two objects and then use restClient.Patch to update it
	var result crdv1.VolumeSnapshot
	err = vs.restClient.Put().
		Name(snapshot.Metadata.Name).
		Resource(crdv1.VolumeSnapshotResourcePlural).
		Namespace(snapshot.Metadata.Namespace).
		Body(snapshotCopy).
		Do().Into(&result)
	if err != nil {
		return nil, fmt.Errorf("Error updating snapshot object %s on the API server: %v", uniqueSnapshotName, err)
	}

	return &result, nil
}
