package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/golang/glog"
	"github.com/wongma7/nfs-provisioner/framework"

	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/apis/storage"
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

// Upstream proposes PV controller will set this on all claims automatically
const annStorageProvisioner = "volume.beta.kubernetes.io/storage-provisioner"

// Our value for annDynamicallyProvisioned
const provisionerName = "matthew/nfs"

// Number of retries when we create a PV object for a provisioned volume.
const createProvisionedPVRetryCount = 5

// Interval between retries when we create a PV object for a provisioned volume.
const createProvisionedPVInterval = 10 * time.Second

type nfsController struct {
	client kubernetes.Interface

	volumeSource     cache.ListerWatcher
	volumeController *framework.Controller
	claimSource      cache.ListerWatcher
	claimController  *framework.Controller
	classSource      cache.ListerWatcher
	classReflector   *cache.Reflector

	volumes cache.Store
	claims  cache.Store
	classes cache.Store

	eventRecorder record.EventRecorder

	// Map of scheduled/running operations.
	runningOperations goroutinemap.GoRoutineMap

	// For testing only: hook to call before an asynchronous operation starts.
	// Not used when set to nil.
	preOperationHook func(operationName string)

	createProvisionedPVRetryCount int
	createProvisionedPVInterval   time.Duration
}

func newNfsController(
	client kubernetes.Interface,
	resyncPeriod time.Duration,
) *nfsController {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&EventSinkImpl{Interface: client.Core().Events(v1.NamespaceAll)})
	var eventRecorder record.EventRecorder
	out, err := exec.Command("hostname").Output()
	if err != nil {
		glog.Errorf("Error getting hostname for specifying it as source of events: %v", err)
		eventRecorder = broadcaster.NewRecorder(v1.EventSource{Component: fmt.Sprintf("nfs-provisioner-%s", string(out))})
	} else {
		eventRecorder = broadcaster.NewRecorder(v1.EventSource{Component: "nfs-provisioner"})
	}

	controller := &nfsController{
		client:                        client,
		eventRecorder:                 eventRecorder,
		runningOperations:             goroutinemap.NewGoRoutineMap(false /* exponentialBackOffOnError */),
		createProvisionedPVRetryCount: createProvisionedPVRetryCount,
		createProvisionedPVInterval:   createProvisionedPVInterval,
	}

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
		&storage.StorageClass{},
		controller.classes,
		resyncPeriod,
	)

	return controller
}

func (ctrl *nfsController) updateVolume(oldObj, newObj interface{}) {
	volume, ok := newObj.(*v1.PersistentVolume)
	if !ok {
		glog.Errorf("Expected PersistentVolume but handler received %#v", newObj)
		return
	}

	if ctrl.shouldDelete(volume) {
		glog.Error("DELETE!")
		opName := fmt.Sprintf("delete-%s[%s]", volume.Name, string(volume.UID))
		ctrl.scheduleOperation(opName, func() error {
			ctrl.deleteVolumeOperation(volume)
			return nil
		})
	}
}

func (ctrl *nfsController) addClaim(obj interface{}) {
	claim, ok := obj.(*v1.PersistentVolumeClaim)
	if !ok {
		glog.Errorf("Expected PersistentVolumeClaim but addClaim received %+v", obj)
		return
	}

	if ctrl.shouldProvision(claim) {
		glog.Error("PROVISION!")
		opName := fmt.Sprintf("provision-%s[%s]", claimToClaimKey(claim), string(claim.UID))
		ctrl.scheduleOperation(opName, func() error {
			ctrl.provisionClaimOperation(claim)
			return nil
		})
	}
}

func (ctrl *nfsController) updateClaim(oldObj, newObj interface{}) {
	ctrl.addClaim(newObj)
}

func (ctrl *nfsController) shouldDelete(volume *v1.PersistentVolume) bool {
	if volume.Status.Phase != v1.VolumeReleased && volume.Status.Phase != v1.VolumeFailed {
		return false
	}

	if hasAnnotation(volume.ObjectMeta, annDynamicallyProvisioned) {
		if ann := volume.Annotations[annDynamicallyProvisioned]; ann != provisionerName {
			return false
		}
	}

	path := fmt.Sprintf("/exports/%s", volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}

	return true
}

func (ctrl *nfsController) shouldProvision(claim *v1.PersistentVolumeClaim) bool {
	// We can do this instead of class.Provisioner != provisionerName below later
	// then we can remove all code below volumename check
	// if claim.Annotations[annStorageProvisioner] != provisionerName {
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
		glog.Errorf("%v", ctrl.classes.List())
		return false
	}
	class, ok := classObj.(*v1beta1.StorageClass)
	if !ok {
		glog.Errorf("Cannot convert object to StorageClass: %+v", classObj)
		return false
	}

	if class.Provisioner != provisionerName {
		return false
	}

	return true
}

func (ctrl *nfsController) provisionClaimOperation(claim *v1.PersistentVolumeClaim) {
	// most code here identical to controller.go. Only, events should say they are coming from THIS controller if multiple controllers are running
	// also theres probably no need to care about verbosity
	claimClass := getClaimClass(claim)
	glog.V(4).Infof("provisionClaimOperation [%s] started, class: %q", claimToClaimKey(claim), claimClass)

	//  A previous doProvisionClaim may just have finished while we were waiting for
	//  the locks. Check that PV (with deterministic name) hasn't been provisioned
	//  yet.
	pvName := ctrl.getProvisionedVolumeNameForClaim(claim)
	volume, err := ctrl.client.Core().PersistentVolumes().Get(pvName)
	if err == nil && volume != nil {
		// Volume has been already provisioned, nothing to do.
		glog.V(4).Infof("provisionClaimOperation [%s]: volume already exists, skipping", claimToClaimKey(claim))
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

	options := VolumeOptions{
		Capacity:                      claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
		AccessModes:                   claim.Spec.AccessModes,
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
		PVName:     pvName,
		Parameters: storageClass.Parameters,
	}

	volume, err = ctrl.provision(options)
	if err != nil {
		strerr := fmt.Sprintf("Failed to provision volume with StorageClass %q: %v", storageClass.Name, err)
		glog.Errorf("Failed to provision volume for claim %q with StorageClass %q: %v", claimToClaimKey(claim), claim.Name, err)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)
		return
	}

	glog.V(3).Infof("volume %q for claim %q created", volume.Name, claimToClaimKey(claim))

	// Set ClaimRef and the PV controller will bind and set annBoundByController for us
	volume.Spec.ClaimRef = claimRef

	setAnnotation(&volume.ObjectMeta, annDynamicallyProvisioned, provisionerName)
	setAnnotation(&volume.ObjectMeta, annClass, claimClass)

	// Try to create the PV object several times
	for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
		glog.V(4).Infof("provisionClaimOperation [%s]: trying to save volume %s", claimToClaimKey(claim), volume.Name)
		if _, err = ctrl.client.Core().PersistentVolumes().Create(volume); err == nil {
			// Save succeeded.
			glog.V(3).Infof("volume %q for claim %q saved", volume.Name, claimToClaimKey(claim))
			break
		}
		// Save failed, try again after a while.
		glog.V(3).Infof("failed to save volume %q for claim %q: %v", volume.Name, claimToClaimKey(claim), err)
		time.Sleep(ctrl.createProvisionedPVInterval)
	}

	if err != nil {
		// Save failed. Now we have a storage asset outside of Kubernetes,
		// but we don't have appropriate PV object for it.
		// Emit some event here and try to delete the storage asset several
		// times.
		strerr := fmt.Sprintf("Error creating provisioned PV object for claim %s: %v. Deleting the volume.", claimToClaimKey(claim), err)
		glog.V(3).Info(strerr)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)

		for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
			if err = ctrl.delete(volume); err == nil {
				// Delete succeeded
				glog.V(4).Infof("provisionClaimOperation [%s]: cleaning volume %s succeeded", claimToClaimKey(claim), volume.Name)
				break
			}
			// Delete failed, try again after a while.
			glog.V(3).Infof("failed to delete volume %q: %v", volume.Name, err)
			time.Sleep(ctrl.createProvisionedPVInterval)
		}

		if err != nil {
			// Delete failed several times. There is an orphaned volume and there
			// is nothing we can do about it.
			strerr := fmt.Sprintf("Error cleaning provisioned volume for claim %s: %v. Please delete manually.", claimToClaimKey(claim), err)
			glog.V(2).Info(strerr)
			ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningCleanupFailed", strerr)
		}
	} else {
		glog.V(2).Infof("volume %q provisioned for claim %q", volume.Name, claimToClaimKey(claim))
	}
}

// VolumeOptions contains option information about a volume
type VolumeOptions struct {
	// Capacity is the size of a volume.
	Capacity resource.Quantity
	// AccessModes of a volume
	AccessModes []v1.PersistentVolumeAccessMode
	// Reclamation policy for a persistent volume
	PersistentVolumeReclaimPolicy v1.PersistentVolumeReclaimPolicy
	// PV.Name of the appropriate PersistentVolume. Used to generate cloud
	// volume name.
	PVName string
	// Volume provisioning parameters from StorageClass
	Parameters map[string]string
}

func (ctrl *nfsController) provision(options VolumeOptions) (*v1.PersistentVolume, error) {
	// instead of createVolume could call out a script of some kind
	server, path, err := ctrl.createVolume(options.PVName)
	if err != nil {
		return nil, err
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:   options.PVName,
			Labels: map[string]string{},
			Annotations: map[string]string{
				"kubernetes.io/createdby": "nfs-dynamic-provisioner",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.Capacity,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

func (ctrl *nfsController) createVolume(PVName string) (string, string, error) {
	path := fmt.Sprintf("/exports/%s", PVName)
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", "", err
	}
	cmd := exec.Command("exportfs", "-o", "rw,insecure,no_root_squash", "*:"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return "", "", fmt.Errorf("Export failed with error: %v, output: %v", err, out)
	}

	out, err = exec.Command("hostname", "-i").Output()
	if err != nil {
		os.RemoveAll(path)
		return "", "", err
	}
	server := string(out)

	return server, path, nil
}

func (ctrl *nfsController) deleteVolumeOperation(volume *v1.PersistentVolume) {
	glog.V(4).Infof("deleteVolumeOperation [%s] started", volume.Name)

	// This method may have been waiting for a volume lock for some time.
	// Our check does not have to be as sophisticated as PV controller's, we can
	// trust that the PV controller has set the PV to Released/Failed and it's
	// ours to delete
	newVolume, err := ctrl.client.Core().PersistentVolumes().Get(volume.Name)
	if err != nil {
		glog.V(3).Infof("error reading peristent volume %q: %v", volume.Name, err)
		return
	}
	if !ctrl.shouldDelete(newVolume) {
		glog.V(3).Infof("volume %q no longer needs deletion, skipping", volume.Name)
		return
	}

	if err := ctrl.delete(volume); err != nil {
		// Delete failed, emit an event.
		glog.V(3).Infof("deletion of volume %q failed: %v", volume.Name, err)
		ctrl.eventRecorder.Event(volume, v1.EventTypeWarning, "VolumeFailedDelete", err.Error())
		return
	}

	glog.V(4).Infof("deleteVolumeOperation [%s]: success", volume.Name)
	// Delete the volume
	if err = ctrl.client.Core().PersistentVolumes().Delete(volume.Name, nil); err != nil {
		// Oops, could not delete the volume and therefore the controller will
		// try to delete the volume again on next update.
		glog.V(3).Infof("failed to delete volume %q from database: %v", volume.Name, err)
		return
	}
	return
}

func (ctrl *nfsController) delete(volume *v1.PersistentVolume) error {
	path := fmt.Sprintf("/exports/%s", volume.ObjectMeta.Name)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("Error deleting volume by removing its path")
	}

	cmd := exec.Command("exportfs", "-u", "*:"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Unexport failed with error: %v, output: %v", err, out)
	}

	return nil
}

// scheduleOperation starts given asynchronous operation on given volume. It
// makes sure the operation is already not running.
func (ctrl *nfsController) scheduleOperation(operationName string, operation func() error) {
	glog.V(4).Infof("scheduleOperation[%s]", operationName)

	// Poke test code that an operation is just about to get started.
	if ctrl.preOperationHook != nil {
		ctrl.preOperationHook(operationName)
	}

	err := ctrl.runningOperations.Run(operationName, operation)
	if err != nil {
		if goroutinemap.IsAlreadyExists(err) {
			glog.V(4).Infof("operation %q is already running, skipping", operationName)
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

func (ctrl *nfsController) Run(stopCh <-chan struct{}) {
	glog.Info("starting nfs controller!")
	go ctrl.claimController.Run(stopCh)
	go ctrl.volumeController.Run(stopCh)
	go ctrl.classReflector.RunUntil(stopCh)
	<-stopCh
}
