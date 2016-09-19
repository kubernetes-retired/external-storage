package controller

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/golang/glog"

	// TODO get rid of this and use https://github.com/kubernetes/kubernetes/pull/32718
	"github.com/wongma7/nfs-provisioner/framework"
	vol "github.com/wongma7/nfs-provisioner/volume"

	"k8s.io/client-go/1.4/kubernetes"
	core_v1 "k8s.io/client-go/1.4/kubernetes/typed/core/v1"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/apis/storage/v1beta1"
	"k8s.io/client-go/1.4/pkg/runtime"
	"k8s.io/client-go/1.4/pkg/watch"
	"k8s.io/client-go/1.4/tools/cache"
	"k8s.io/client-go/1.4/tools/record"

	"k8s.io/kubernetes/pkg/util/goroutinemap"
)

// annClass annotation represents the storage class associated with a resource:
// - in PersistentVolumeClaim it represents required class to match.
//   Only PersistentVolumes with the same class (i.e. annotation with the same
//   value) can be bound to the claim. In case no such volume exists, the
//   controller will provision a new one using StorageClass instance with
//   the same name as the annotation value.
// - in PersistentVolume it represents storage class to which the persistent
//   volume belongs.
const annClass = "volume.beta.kubernetes.io/storage-class"

// This annotation is added to a PV that has been dynamically provisioned by
// Kubernetes. Its value is name of volume plugin that created the volume.
// It serves both user (to show where a PV comes from) and Kubernetes (to
// recognize dynamically provisioned PVs in its decisions).
const annDynamicallyProvisioned = "pv.kubernetes.io/provisioned-by"

// TODO upstream proposes PV controller will set this on all claims automatically
// https://github.com/kubernetes/kubernetes/pull/30285
const annStorageProvisioner = "volume.beta.kubernetes.io/storage-provisioner"

// Number of retries when we create a PV object for a provisioned volume.
const createProvisionedPVRetryCount = 5

// Interval between retries when we create a PV object for a provisioned volume.
const createProvisionedPVInterval = 10 * time.Second

type nfsController struct {
	client kubernetes.Interface

	// The name of the provisioner for which this controller dynamically
	// provisions volumes. The value of annDynamicallyProvisioned and
	// annStorageProvisioner to set & watch for, respectively
	provisioner string

	claimSource      cache.ListerWatcher
	claimController  *framework.Controller
	volumeSource     cache.ListerWatcher
	volumeController *framework.Controller
	classSource      cache.ListerWatcher
	classReflector   *cache.Reflector

	volumes cache.Store
	claims  cache.Store
	classes cache.Store

	eventRecorder record.EventRecorder

	// Map of scheduled/running operations.
	runningOperations goroutinemap.GoRoutineMap

	createProvisionedPVRetryCount int
	createProvisionedPVInterval   time.Duration
}

func NewNfsController(
	client kubernetes.Interface,
	resyncPeriod time.Duration,
	provisioner string,
) *nfsController {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&core_v1.EventSinkImpl{Interface: client.Core().Events(v1.NamespaceAll)})
	var eventRecorder record.EventRecorder
	out, err := exec.Command("hostname").Output()
	if err != nil {
		glog.Errorf("Error getting hostname for specifying it as source of events: %v", err)
		eventRecorder = broadcaster.NewRecorder(v1.EventSource{Component: provisioner})
	} else {
		eventRecorder = broadcaster.NewRecorder(v1.EventSource{Component: fmt.Sprintf("%s-%s", provisioner, strings.TrimSpace(string(out)))})
	}

	controller := &nfsController{
		client:                        client,
		provisioner:                   provisioner,
		eventRecorder:                 eventRecorder,
		runningOperations:             goroutinemap.NewGoRoutineMap(false /* exponentialBackOffOnError */),
		createProvisionedPVRetryCount: createProvisionedPVRetryCount,
		createProvisionedPVInterval:   createProvisionedPVInterval,
	}

	controller.claimSource = &cache.ListWatch{
		ListFunc: func(options api.ListOptions) (runtime.Object, error) {
			return client.Core().PersistentVolumeClaims(v1.NamespaceAll).List(options)
		},
		WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
			return client.Core().PersistentVolumeClaims(v1.NamespaceAll).Watch(options)
		},
	}
	controller.claims, controller.claimController = framework.NewInformer(
		controller.claimSource,
		&v1.PersistentVolumeClaim{},
		resyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc:    controller.addClaim,
			UpdateFunc: controller.updateClaim,
			DeleteFunc: nil,
		},
	)

	controller.volumeSource = &cache.ListWatch{
		ListFunc: func(options api.ListOptions) (runtime.Object, error) {
			return client.Core().PersistentVolumes().List(options)
		},
		WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
			return client.Core().PersistentVolumes().Watch(options)
		},
	}
	controller.volumes, controller.volumeController = framework.NewInformer(
		controller.volumeSource,
		&v1.PersistentVolume{},
		resyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc:    nil,
			UpdateFunc: controller.updateVolume,
			DeleteFunc: nil,
		},
	)

	controller.classSource = &cache.ListWatch{
		ListFunc: func(options api.ListOptions) (runtime.Object, error) {
			return client.Storage().StorageClasses().List(options)
		},
		WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
			return client.Storage().StorageClasses().Watch(options)
		},
	}
	controller.classes = cache.NewStore(framework.DeletionHandlingMetaNamespaceKeyFunc)
	controller.classReflector = cache.NewReflector(
		controller.classSource,
		&v1beta1.StorageClass{},
		controller.classes,
		resyncPeriod,
	)

	return controller
}

func (ctrl *nfsController) Run(stopCh <-chan struct{}) {
	glog.Info("Starting nfs provisioner controller!")
	go ctrl.claimController.Run(stopCh)
	go ctrl.volumeController.Run(stopCh)
	go ctrl.classReflector.RunUntil(stopCh)
	<-stopCh
}

// On add claim, check if the added claim should have a volume provisioned for
// it and provision one if so.
func (ctrl *nfsController) addClaim(obj interface{}) {
	claim, ok := obj.(*v1.PersistentVolumeClaim)
	if !ok {
		glog.Errorf("Expected PersistentVolumeClaim but addClaim received %+v", obj)
		return
	}

	if ctrl.shouldProvision(claim) {
		opName := fmt.Sprintf("provision-%s[%s]", claimToClaimKey(claim), string(claim.UID))
		ctrl.scheduleOperation(opName, func() error {
			ctrl.provisionClaimOperation(claim)
			return nil
		})
	}
}

// On update claim, pass the new claim to addClaim. Updates occur at least every
// resyncPeriod.
func (ctrl *nfsController) updateClaim(oldObj, newObj interface{}) {
	ctrl.addClaim(newObj)
}

// On update volume, check if the updated volume should be deleted and delete if
// so. Updates occur at least every resyncPeriod.
func (ctrl *nfsController) updateVolume(oldObj, newObj interface{}) {
	volume, ok := newObj.(*v1.PersistentVolume)
	if !ok {
		glog.Errorf("Expected PersistentVolume but handler received %#v", newObj)
		return
	}

	if ctrl.shouldDelete(volume) {
		opName := fmt.Sprintf("delete-%s[%s]", volume.Name, string(volume.UID))
		ctrl.scheduleOperation(opName, func() error {
			ctrl.deleteVolumeOperation(volume)
			return nil
		})
	}
}

func (ctrl *nfsController) shouldProvision(claim *v1.PersistentVolumeClaim) bool {
	// TODO do this and remove all code below VolumeName check
	// https://github.com/kubernetes/kubernetes/pull/30285
	// if claim.Annotations[annStorageProvisioner] != provisioner {
	// 	return false, nil
	// }

	if claim.Spec.VolumeName != "" {
		return false
	}

	claimClass := getClaimClass(claim)
	classObj, found, err := ctrl.classes.GetByKey(claimClass)
	if err != nil {
		glog.Errorf("Error getting StorageClass %q: %v", claimClass, err)
		return false
	}
	if !found {
		glog.Errorf("StorageClass %q not found", claimClass)
		return false
	}
	class, ok := classObj.(*v1beta1.StorageClass)
	if !ok {
		glog.Errorf("Cannot convert object to StorageClass: %+v", classObj)
		return false
	}

	if class.Provisioner != ctrl.provisioner {
		return false
	}

	return true
}

func (ctrl *nfsController) shouldDelete(volume *v1.PersistentVolume) bool {
	// TODO https://github.com/kubernetes/kubernetes/pull/32565 will not Fail PVs
	if volume.Status.Phase != v1.VolumeReleased && volume.Status.Phase != v1.VolumeFailed {
		return false
	}

	if !hasAnnotation(volume.ObjectMeta, annDynamicallyProvisioned) {
		return false
	}

	if ann := volume.Annotations[annDynamicallyProvisioned]; ann != ctrl.provisioner {
		return false
	}

	// Check if the volume even exists for deletion rather than trying, failing,
	// and sending a bad event to the PV.
	if !vol.Exists(volume) {
		return false
	}

	return true
}

func (ctrl *nfsController) provisionClaimOperation(claim *v1.PersistentVolumeClaim) {
	// Most code here is identical to that found in controller.go of kube's PV controller...
	claimClass := getClaimClass(claim)
	glog.Infof("provisionClaimOperation [%s] started, class: %q", claimToClaimKey(claim), claimClass)

	//  A previous doProvisionClaim may just have finished while we were waiting for
	//  the locks. Check that PV (with deterministic name) hasn't been provisioned
	//  yet.
	pvName := ctrl.getProvisionedVolumeNameForClaim(claim)
	volume, err := ctrl.client.Core().PersistentVolumes().Get(pvName)
	if err == nil && volume != nil {
		// Volume has been already provisioned, nothing to do.
		glog.Infof("provisionClaimOperation [%s]: volume already exists, skipping", claimToClaimKey(claim))
		return
	}

	// Prepare a claimRef to the claim early (to fail before a volume is
	// provisioned)
	claimRef, err := v1.GetReference(claim)
	if err != nil {
		glog.Errorf("unexpected error getting claim reference: %v", err)
		return
	}

	classObj, found, err := ctrl.classes.GetByKey(claimClass)
	if err != nil {
		glog.Errorf("Error getting StorageClass %q: %v", claimClass, err)
		return
	}
	if !found {
		glog.Errorf("StorageClass %q not found", claimClass)
		return
	}
	storageClass, ok := classObj.(*v1beta1.StorageClass)
	if !ok {
		glog.Errorf("Cannot convert object to StorageClass: %+v", classObj)
		return
	}

	options := vol.VolumeOptions{
		Capacity:                      claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
		AccessModes:                   claim.Spec.AccessModes,
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
		PVName:     pvName,
		Parameters: storageClass.Parameters,
	}

	volume, err = vol.Provision(options)
	if err != nil {
		strerr := fmt.Sprintf("Failed to provision volume with StorageClass %q: %v", storageClass.Name, err)
		glog.Errorf("Failed to provision volume for claim %q with StorageClass %q: %v", claimToClaimKey(claim), claim.Name, err)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)
		return
	}

	glog.Infof("volume %q for claim %q created", volume.Name, claimToClaimKey(claim))

	// Set ClaimRef and the PV controller will bind and set annBoundByController for us
	volume.Spec.ClaimRef = claimRef

	setAnnotation(&volume.ObjectMeta, annDynamicallyProvisioned, ctrl.provisioner)
	setAnnotation(&volume.ObjectMeta, annClass, claimClass)

	// Try to create the PV object several times
	for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
		glog.Infof("provisionClaimOperation [%s]: trying to save volume %s", claimToClaimKey(claim), volume.Name)
		if _, err = ctrl.client.Core().PersistentVolumes().Create(volume); err == nil {
			// Save succeeded.
			glog.Infof("volume %q for claim %q saved", volume.Name, claimToClaimKey(claim))
			break
		}
		// Save failed, try again after a while.
		glog.Infof("failed to save volume %q for claim %q: %v", volume.Name, claimToClaimKey(claim), err)
		time.Sleep(ctrl.createProvisionedPVInterval)
	}

	if err != nil {
		// Save failed. Now we have a storage asset outside of Kubernetes,
		// but we don't have appropriate PV object for it.
		// Emit some event here and try to delete the storage asset several
		// times.
		strerr := fmt.Sprintf("Error creating provisioned PV object for claim %s: %v. Deleting the volume.", claimToClaimKey(claim), err)
		glog.Info(strerr)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)

		for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
			if err = vol.Delete(volume); err == nil {
				// Delete succeeded
				glog.Infof("provisionClaimOperation [%s]: cleaning volume %s succeeded", claimToClaimKey(claim), volume.Name)
				break
			}
			// Delete failed, try again after a while.
			glog.Infof("failed to delete volume %q: %v", volume.Name, err)
			time.Sleep(ctrl.createProvisionedPVInterval)
		}

		if err != nil {
			// Delete failed several times. There is an orphaned volume and there
			// is nothing we can do about it.
			strerr := fmt.Sprintf("Error cleaning provisioned volume for claim %s: %v. Please delete manually.", claimToClaimKey(claim), err)
			glog.Info(strerr)
			ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningCleanupFailed", strerr)
		}
	} else {
		glog.Infof("volume %q provisioned for claim %q", volume.Name, claimToClaimKey(claim))
	}
}

func (ctrl *nfsController) deleteVolumeOperation(volume *v1.PersistentVolume) {
	glog.Infof("deleteVolumeOperation [%s] started", volume.Name)

	// This method may have been waiting for a volume lock for some time.
	// Our check does not have to be as sophisticated as PV controller's, we can
	// trust that the PV controller has set the PV to Released/Failed and it's
	// ours to delete
	newVolume, err := ctrl.client.Core().PersistentVolumes().Get(volume.Name)
	if err != nil {
		glog.Infof("error reading peristent volume %q: %v", volume.Name, err)
		return
	}
	if !ctrl.shouldDelete(newVolume) {
		glog.Infof("volume %q no longer needs deletion, skipping", volume.Name)
		return
	}

	if err := vol.Delete(volume); err != nil {
		// Delete failed, emit an event.
		glog.Infof("deletion of volume %q failed: %v", volume.Name, err)
		ctrl.eventRecorder.Event(volume, v1.EventTypeWarning, "VolumeFailedDelete", err.Error())
		return
	}

	glog.Infof("deleteVolumeOperation [%s]: success", volume.Name)
	// Delete the volume
	if err = ctrl.client.Core().PersistentVolumes().Delete(volume.Name, nil); err != nil {
		// Oops, could not delete the volume and therefore the controller will
		// try to delete the volume again on next update.
		glog.Infof("failed to delete volume %q from database: %v", volume.Name, err)
		return
	}

	return
}

// scheduleOperation starts given asynchronous operation on given volume. It
// makes sure the operation is already not running.
func (ctrl *nfsController) scheduleOperation(operationName string, operation func() error) {
	glog.Infof("scheduleOperation[%s]", operationName)

	err := ctrl.runningOperations.Run(operationName, operation)
	if err != nil {
		if goroutinemap.IsAlreadyExists(err) {
			glog.Infof("operation %q is already running, skipping", operationName)
		} else {
			glog.Errorf("error scheduling operaion %q: %v", operationName, err)
		}
	}
}

func hasAnnotation(obj v1.ObjectMeta, ann string) bool {
	_, found := obj.Annotations[ann]
	return found
}

func setAnnotation(obj *v1.ObjectMeta, ann string, value string) {
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	obj.Annotations[ann] = value
}

// getClaimClass returns name of class that is requested by given claim.
// Request for `nil` class is interpreted as request for class "",
// i.e. for a classless PV.
// controller_base.go
func getClaimClass(claim *v1.PersistentVolumeClaim) string {
	// TODO: change to PersistentVolumeClaim.Spec.Class value when this
	// attribute is introduced.
	if class, found := claim.Annotations[annClass]; found {
		return class
	}

	return ""
}

// getProvisionedVolumeNameForClaim returns PV.Name for the provisioned volume.
// The name must be unique.
func (ctrl *nfsController) getProvisionedVolumeNameForClaim(claim *v1.PersistentVolumeClaim) string {
	return "pvc-" + string(claim.UID)
}

func claimToClaimKey(claim *v1.PersistentVolumeClaim) string {
	return fmt.Sprintf("%s/%s", claim.Namespace, claim.Name)
}

func claimrefToClaimKey(claimref *v1.ObjectReference) string {
	return fmt.Sprintf("%s/%s", claimref.Namespace, claimref.Name)
}
