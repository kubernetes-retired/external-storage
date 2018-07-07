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

package controller

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller/metrics"
	"github.com/kubernetes-incubator/external-storage/lib/leaderelection"
	rl "github.com/kubernetes-incubator/external-storage/lib/leaderelection/resourcelock"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
	"k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
	storagebeta "k8s.io/api/storage/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	ref "k8s.io/client-go/tools/reference"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
	utilversion "k8s.io/kubernetes/pkg/util/version"
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

const annStorageProvisioner = "volume.beta.kubernetes.io/storage-provisioner"

// ProvisionController is a controller that provisions PersistentVolumes for
// PersistentVolumeClaims.
type ProvisionController struct {
	client kubernetes.Interface

	// The name of the provisioner for which this controller dynamically
	// provisions volumes. The value of annDynamicallyProvisioned and
	// annStorageProvisioner to set & watch for, respectively
	provisionerName string

	// The provisioner the controller will use to provision and delete volumes.
	// Presumably this implementer of Provisioner carries its own
	// volume-specific options and such that it needs in order to provision
	// volumes.
	provisioner Provisioner

	// Kubernetes cluster server version:
	// * 1.4: storage classes introduced as beta. Technically out-of-tree dynamic
	// provisioning is not officially supported, though it works
	// * 1.5: storage classes stay in beta. Out-of-tree dynamic provisioning is
	// officially supported
	// * 1.6: storage classes enter GA
	kubeVersion *utilversion.Version

	// TODO remove this
	claimSource cache.ListerWatcher

	claimInformer    cache.SharedInformer
	claims           cache.Store
	claimController  cache.Controller
	volumeInformer   cache.SharedInformer
	volumes          cache.Store
	volumeController cache.Controller
	classInformer    cache.SharedInformer
	classes          cache.Store
	classController  cache.Controller

	claimQueue  workqueue.RateLimitingInterface
	volumeQueue workqueue.RateLimitingInterface

	// Identity of this controller, generated at creation time and not persisted
	// across restarts. Useful only for debugging, for seeing the source of
	// events. controller.provisioner may have its own, different notion of
	// identity which may/may not persist across restarts
	identity      types.UID
	eventRecorder record.EventRecorder

	resyncPeriod time.Duration

	exponentialBackOffOnError bool
	threadiness               int

	createProvisionedPVRetryCount int
	createProvisionedPVInterval   time.Duration

	failedProvisionThreshold, failedDeleteThreshold int

	// The port for metrics server to serve on.
	metricsPort int32
	// The IP address for metrics server to serve on.
	metricsAddress string
	// The path of metrics endpoint path.
	metricsPath string

	// Parameters of leaderelection.LeaderElectionConfig. Leader election is for
	// when multiple controllers are running: they race to lock (lead) every PVC
	// so that only one calls Provision for it (saving API calls, CPU cycles...)
	leaseDuration, renewDeadline, retryPeriod, termLimit time.Duration
	// Map of claim UID to LeaderElector: for checking if this controller
	// is the leader of a given claim
	leaderElectors      map[types.UID]*leaderelection.LeaderElector
	leaderElectorsMutex *sync.Mutex

	hasRun     bool
	hasRunLock *sync.Mutex
}

const (
	// DefaultResyncPeriod is used when option function ResyncPeriod is omitted
	DefaultResyncPeriod = 15 * time.Minute
	// DefaultThreadiness is used when option function Threadiness is omitted
	DefaultThreadiness = 4
	// DefaultExponentialBackOffOnError is used when option function ExponentialBackOffOnError is omitted
	DefaultExponentialBackOffOnError = true
	// DefaultCreateProvisionedPVRetryCount is used when option function CreateProvisionedPVRetryCount is omitted
	DefaultCreateProvisionedPVRetryCount = 5
	// DefaultCreateProvisionedPVInterval is used when option function CreateProvisionedPVInterval is omitted
	DefaultCreateProvisionedPVInterval = 10 * time.Second
	// DefaultFailedProvisionThreshold is used when option function FailedProvisionThreshold is omitted
	DefaultFailedProvisionThreshold = 15
	// DefaultFailedDeleteThreshold is used when option function FailedDeleteThreshold is omitted
	DefaultFailedDeleteThreshold = 15
	// DefaultLeaseDuration is used when option function LeaseDuration is omitted
	DefaultLeaseDuration = 15 * time.Second
	// DefaultRenewDeadline is used when option function RenewDeadline is omitted
	DefaultRenewDeadline = 10 * time.Second
	// DefaultRetryPeriod is used when option function RetryPeriod is omitted
	DefaultRetryPeriod = 2 * time.Second
	// DefaultTermLimit is used when option function TermLimit is omitted
	DefaultTermLimit = 30 * time.Second
	// DefaultMetricsPort is used when option function MetricsPort is omitted
	DefaultMetricsPort = 0
	// DefaultMetricsAddress is used when option function MetricsAddress is omitted
	DefaultMetricsAddress = "0.0.0.0"
	// DefaultMetricsPath is used when option function MetricsPath is omitted
	DefaultMetricsPath = "/metrics"
)

var errRuntime = fmt.Errorf("cannot call option functions after controller has Run")

// ResyncPeriod is how often the controller relists PVCs, PVs, & storage
// classes. OnUpdate will be called even if nothing has changed, meaning failed
// operations may be retried on a PVC/PV every resyncPeriod regardless of
// whether it changed. Defaults to 15 minutes.
func ResyncPeriod(resyncPeriod time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.resyncPeriod = resyncPeriod
		return nil
	}
}

// Threadiness is the number of claim and volume workers each to launch.
// Defaults to 4.
func Threadiness(threadiness int) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.threadiness = threadiness
		return nil
	}
}

// ExponentialBackOffOnError determines whether to exponentially back off from
// failures of Provision and Delete. Defaults to true.
func ExponentialBackOffOnError(exponentialBackOffOnError bool) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.exponentialBackOffOnError = exponentialBackOffOnError
		return nil
	}
}

// CreateProvisionedPVRetryCount is the number of retries when we create a PV
// object for a provisioned volume. Defaults to 5.
func CreateProvisionedPVRetryCount(createProvisionedPVRetryCount int) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.createProvisionedPVRetryCount = createProvisionedPVRetryCount
		return nil
	}
}

// CreateProvisionedPVInterval is the interval between retries when we create a
// PV object for a provisioned volume. Defaults to 10 seconds.
func CreateProvisionedPVInterval(createProvisionedPVInterval time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.createProvisionedPVInterval = createProvisionedPVInterval
		return nil
	}
}

// FailedProvisionThreshold is the threshold for max number of retries on
// failures of Provision. Defaults to 15.
func FailedProvisionThreshold(failedProvisionThreshold int) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.failedProvisionThreshold = failedProvisionThreshold
		return nil
	}
}

// FailedDeleteThreshold is the threshold for max number of retries on failures
// of Delete. Defaults to 15.
func FailedDeleteThreshold(failedDeleteThreshold int) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.failedDeleteThreshold = failedDeleteThreshold
		return nil
	}
}

// LeaseDuration is the duration that non-leader candidates will
// wait to force acquire leadership. This is measured against time of
// last observed ack. Defaults to 15 seconds.
func LeaseDuration(leaseDuration time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.leaseDuration = leaseDuration
		return nil
	}
}

// RenewDeadline is the duration that the acting master will retry
// refreshing leadership before giving up. Defaults to 10 seconds.
func RenewDeadline(renewDeadline time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.renewDeadline = renewDeadline
		return nil
	}
}

// RetryPeriod is the duration the LeaderElector clients should wait
// between tries of actions. Defaults to 2 seconds.
func RetryPeriod(retryPeriod time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.retryPeriod = retryPeriod
		return nil
	}
}

// TermLimit is the maximum duration that a leader may remain the leader
// to complete the task before it must give up its leadership. 0 for forever
// or indefinite. Defaults to 30 seconds.
func TermLimit(termLimit time.Duration) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.termLimit = termLimit
		return nil
	}
}

// ClaimsInformer sets the informer to use for accessing PersistentVolumeClaims.
// Defaults to using a private (non-shared) informer.
func ClaimsInformer(informer cache.SharedInformer) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.claimInformer = informer
		return nil
	}
}

// VolumesInformer sets the informer to use for accessing PersistentVolumes.
// Defaults to using a private (non-shared) informer.
func VolumesInformer(informer cache.SharedInformer) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.volumeInformer = informer
		return nil
	}
}

// ClassesInformer sets the informer to use for accessing StorageClasses.
// The informer must use the versioned resource appropriate for the Kubernetes cluster version
// (that is, v1.StorageClass for >= 1.6, and v1beta1.StorageClass for < 1.6).
// Defaults to using a private (non-shared) informer.
func ClassesInformer(informer cache.SharedInformer) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.classInformer = informer
		return nil
	}
}

// MetricsPort sets the port that metrics server serves on. Default: 0, set to non-zero to enable.
func MetricsPort(metricsPort int32) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.metricsPort = metricsPort
		return nil
	}
}

// MetricsAddress sets the ip address that metrics serve serves on.
func MetricsAddress(metricsAddress string) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.metricsAddress = metricsAddress
		return nil
	}
}

// MetricsPath sets the endpoint path of metrics server.
func MetricsPath(metricsPath string) func(*ProvisionController) error {
	return func(c *ProvisionController) error {
		if c.HasRun() {
			return errRuntime
		}
		c.metricsPath = metricsPath
		return nil
	}
}

// HasRun returns whether the controller has Run
func (ctrl *ProvisionController) HasRun() bool {
	ctrl.hasRunLock.Lock()
	defer ctrl.hasRunLock.Unlock()
	return ctrl.hasRun
}

// NewProvisionController creates a new provision controller using
// the given configuration parameters and with private (non-shared) informers.
func NewProvisionController(
	client kubernetes.Interface,
	provisionerName string,
	provisioner Provisioner,
	kubeVersion string,
	options ...func(*ProvisionController) error,
) *ProvisionController {
	identity := uuid.NewUUID()

	v1.AddToScheme(scheme.Scheme)
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(glog.Infof)
	broadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: client.CoreV1().Events(v1.NamespaceAll)})
	var eventRecorder record.EventRecorder
	out, err := exec.Command("hostname").Output()
	if err != nil {
		eventRecorder = broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: fmt.Sprintf("%s %s", provisionerName, string(identity))})
	} else {
		eventRecorder = broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: fmt.Sprintf("%s %s %s", provisionerName, strings.TrimSpace(string(out)), string(identity))})
	}

	controller := &ProvisionController{
		client:                        client,
		provisionerName:               provisionerName,
		provisioner:                   provisioner,
		kubeVersion:                   utilversion.MustParseSemantic(kubeVersion),
		identity:                      identity,
		eventRecorder:                 eventRecorder,
		resyncPeriod:                  DefaultResyncPeriod,
		threadiness:                   DefaultThreadiness,
		createProvisionedPVRetryCount: DefaultCreateProvisionedPVRetryCount,
		createProvisionedPVInterval:   DefaultCreateProvisionedPVInterval,
		failedProvisionThreshold:      DefaultFailedProvisionThreshold,
		failedDeleteThreshold:         DefaultFailedDeleteThreshold,
		leaseDuration:                 DefaultLeaseDuration,
		renewDeadline:                 DefaultRenewDeadline,
		retryPeriod:                   DefaultRetryPeriod,
		termLimit:                     DefaultTermLimit,
		metricsPort:                   DefaultMetricsPort,
		metricsAddress:                DefaultMetricsAddress,
		metricsPath:                   DefaultMetricsPath,
		leaderElectors:                make(map[types.UID]*leaderelection.LeaderElector),
		leaderElectorsMutex:           &sync.Mutex{},
		hasRun:                        false,
		hasRunLock:                    &sync.Mutex{},
	}

	for _, option := range options {
		option(controller)
	}

	ratelimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(15*time.Second, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
	if !controller.exponentialBackOffOnError {
		ratelimiter = workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
		)
	}
	controller.claimQueue = workqueue.NewNamedRateLimitingQueue(ratelimiter, "claims")
	controller.volumeQueue = workqueue.NewNamedRateLimitingQueue(ratelimiter, "volumes")

	// ----------------------
	// PersistentVolumeClaims

	claimSource := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().PersistentVolumeClaims(v1.NamespaceAll).List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().PersistentVolumeClaims(v1.NamespaceAll).Watch(options)
		},
	}
	controller.claimSource = claimSource

	claimHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { controller.enqueueWork(controller.claimQueue, obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { controller.enqueueWork(controller.claimQueue, newObj) },
		DeleteFunc: func(obj interface{}) { controller.forgetWork(controller.claimQueue, obj) },
	}

	if controller.claimInformer != nil {
		controller.claimInformer.AddEventHandlerWithResyncPeriod(claimHandler, controller.resyncPeriod)
		controller.claims, controller.claimController =
			controller.claimInformer.GetStore(),
			controller.claimInformer.GetController()
	} else {
		controller.claims, controller.claimController =
			cache.NewInformer(
				claimSource,
				&v1.PersistentVolumeClaim{},
				controller.resyncPeriod,
				claimHandler,
			)
	}

	// -----------------
	// PersistentVolumes

	volumeSource := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().PersistentVolumes().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().PersistentVolumes().Watch(options)
		},
	}

	volumeHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { controller.enqueueWork(controller.volumeQueue, obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { controller.enqueueWork(controller.volumeQueue, newObj) },
		DeleteFunc: func(obj interface{}) { controller.forgetWork(controller.volumeQueue, obj) },
	}

	if controller.volumeInformer != nil {
		controller.volumeInformer.AddEventHandlerWithResyncPeriod(volumeHandler, controller.resyncPeriod)
		controller.volumes, controller.volumeController =
			controller.volumeInformer.GetStore(),
			controller.volumeInformer.GetController()
	} else {
		controller.volumes, controller.volumeController =
			cache.NewInformer(
				volumeSource,
				&v1.PersistentVolume{},
				controller.resyncPeriod,
				volumeHandler,
			)
	}

	// --------------
	// StorageClasses

	var versionedClassType runtime.Object
	var classSource cache.ListerWatcher
	if controller.kubeVersion.AtLeast(utilversion.MustParseSemantic("v1.6.0")) {
		versionedClassType = &storage.StorageClass{}
		classSource = &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.StorageV1().StorageClasses().List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.StorageV1().StorageClasses().Watch(options)
			},
		}
	} else {
		versionedClassType = &storagebeta.StorageClass{}
		classSource = &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.StorageV1beta1().StorageClasses().List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.StorageV1beta1().StorageClasses().Watch(options)
			},
		}
	}

	classHandler := cache.ResourceEventHandlerFuncs{
		// We don't need an actual event handler for StorageClasses,
		// but we must pass a non-nil one to cache.NewInformer()
		AddFunc:    nil,
		UpdateFunc: nil,
		DeleteFunc: nil,
	}

	if controller.classInformer != nil {
		// no resource event handler needed for StorageClasses
		controller.classes, controller.classController =
			controller.classInformer.GetStore(),
			controller.classInformer.GetController()
	} else {
		controller.classes, controller.classController = cache.NewInformer(
			classSource,
			versionedClassType,
			controller.resyncPeriod,
			classHandler,
		)
	}

	return controller
}

// enqueueWork takes an obj and converts it into a namespace/name string which
// is then put onto the given work queue.
func (ctrl *ProvisionController) enqueueWork(queue workqueue.RateLimitingInterface, obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	// Re-Adding is harmless but try to add it to the queue only if it is not
	// already there, because if it is already there we *must* be retrying it
	if queue.NumRequeues(key) == 0 {
		queue.Add(key)
	}
}

// forgetWork Forgets an obj from the given work queue, telling the queue to
// stop tracking its retries because e.g. the obj was deleted
func (ctrl *ProvisionController) forgetWork(queue workqueue.RateLimitingInterface, obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	queue.Forget(key)
	queue.Done(key)
}

// Run starts all of this controller's control loops
func (ctrl *ProvisionController) Run(stopCh <-chan struct{}) {
	glog.Infof("Starting provisioner controller %s!", string(ctrl.identity))
	defer utilruntime.HandleCrash()
	defer ctrl.claimQueue.ShutDown()
	defer ctrl.volumeQueue.ShutDown()

	ctrl.hasRunLock.Lock()
	ctrl.hasRun = true
	ctrl.hasRunLock.Unlock()
	if ctrl.metricsPort > 0 {
		prometheus.MustRegister([]prometheus.Collector{
			metrics.PersistentVolumeClaimProvisionTotal,
			metrics.PersistentVolumeClaimProvisionFailedTotal,
			metrics.PersistentVolumeClaimProvisionDurationSeconds,
			metrics.PersistentVolumeDeleteTotal,
			metrics.PersistentVolumeDeleteFailedTotal,
			metrics.PersistentVolumeDeleteDurationSeconds,
		}...)
		http.Handle(ctrl.metricsPath, promhttp.Handler())
		address := net.JoinHostPort(ctrl.metricsAddress, strconv.FormatInt(int64(ctrl.metricsPort), 10))
		glog.Infof("Starting metrics server at %s\n", address)
		go wait.Forever(func() {
			err := http.ListenAndServe(address, nil)
			if err != nil {
				glog.Errorf("Failed to listen on %s: %v", address, err)
			}
		}, 5*time.Second)
	}

	go ctrl.claimController.Run(stopCh)
	go ctrl.volumeController.Run(stopCh)
	go ctrl.classController.Run(stopCh)

	for i := 0; i < ctrl.threadiness; i++ {
		go wait.Until(ctrl.runClaimWorker, time.Second, stopCh)
		go wait.Until(ctrl.runVolumeWorker, time.Second, stopCh)
	}

	glog.Infof("Started provisioner controller %s!", string(ctrl.identity))

	<-stopCh
}

func (ctrl *ProvisionController) runClaimWorker() {
	for ctrl.processNextClaimWorkItem() {
	}
}

func (ctrl *ProvisionController) runVolumeWorker() {
	for ctrl.processNextVolumeWorkItem() {
	}
}

// processNextClaimWorkItem processes items from claimQueue
func (ctrl *ProvisionController) processNextClaimWorkItem() bool {
	obj, shutdown := ctrl.claimQueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer ctrl.claimQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			ctrl.claimQueue.Forget(obj)
			return fmt.Errorf("expected string in workqueue but got %#v", obj)
		}

		if err := ctrl.syncClaimHandler(key); err != nil {
			if ctrl.claimQueue.NumRequeues(obj) < ctrl.failedProvisionThreshold {
				glog.Warningf("retrying syncing claim %q because failures %v < threshold %v", key, ctrl.claimQueue.NumRequeues(obj), ctrl.failedProvisionThreshold)
				ctrl.claimQueue.AddRateLimited(obj)
			} else {
				glog.Errorf("giving up syncing claim %q because failures %v >= threshold %v", key, ctrl.claimQueue.NumRequeues(obj), ctrl.failedProvisionThreshold)
				// Done but do not Forget: it will not be in the queue but NumRequeues
				// will be saved until the obj is deleted from kubernetes
			}
			return fmt.Errorf("error syncing claim %q: %s", key, err.Error())
		}

		ctrl.claimQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// processNextVolumeWorkItem processes items from volumeQueue
func (ctrl *ProvisionController) processNextVolumeWorkItem() bool {
	obj, shutdown := ctrl.volumeQueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer ctrl.volumeQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			ctrl.volumeQueue.Forget(obj)
			return fmt.Errorf("expected string in workqueue but got %#v", obj)
		}

		if err := ctrl.syncVolumeHandler(key); err != nil {
			if ctrl.volumeQueue.NumRequeues(obj) < ctrl.failedDeleteThreshold {
				glog.Warningf("retrying syncing volume %q because failures %v < threshold %v", key, ctrl.volumeQueue.NumRequeues(obj), ctrl.failedProvisionThreshold)
				ctrl.volumeQueue.AddRateLimited(obj)
			} else {
				glog.Errorf("giving up syncing volume %q because failures %v >= threshold %v", key, ctrl.volumeQueue.NumRequeues(obj), ctrl.failedProvisionThreshold)
				// Done but do not Forget: it will not be in the queue but NumRequeues
				// will be saved until the obj is deleted from kubernetes
			}
			return fmt.Errorf("error syncing volume %q: %s", key, err.Error())
		}

		ctrl.volumeQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncClaimHandler gets the claim from informer's cache then calls syncClaim
func (ctrl *ProvisionController) syncClaimHandler(key string) error {
	claimObj, exists, err := ctrl.claims.GetByKey(key)
	if err != nil {
		return err
	}
	if !exists {
		utilruntime.HandleError(fmt.Errorf("claim %q in work queue no longer exists", key))
		return nil
	}

	return ctrl.syncClaim(claimObj)
}

// syncVolumeHandler gets the volume from informer's cache then calls syncVolume
func (ctrl *ProvisionController) syncVolumeHandler(key string) error {
	volumeObj, exists, err := ctrl.volumes.GetByKey(key)
	if err != nil {
		return err
	}
	if !exists {
		utilruntime.HandleError(fmt.Errorf("volume %q in work queue no longer exists", key))
		return nil
	}

	return ctrl.syncVolume(volumeObj)
}

// syncClaim checks if the claim should have a volume provisioned for it and
// provisions one if so.
func (ctrl *ProvisionController) syncClaim(obj interface{}) error {
	claim, ok := obj.(*v1.PersistentVolumeClaim)
	if !ok {
		return fmt.Errorf("expected claim but got %+v", obj)
	}

	if ctrl.shouldProvision(claim) {
		ctrl.leaderElectorsMutex.Lock()
		le, ok := ctrl.leaderElectors[claim.UID]
		ctrl.leaderElectorsMutex.Unlock()
		if ok && le.IsLeader() {
			startTime := time.Now()
			err := ctrl.provisionClaimOperation(claim)
			ctrl.updateProvisionStats(claim, err, startTime)
			return err
		}
		err := ctrl.lockProvisionClaimOperation(claim)
		return err
	}
	return nil
}

// syncVolume checks if the volume should be deleted and deletes if so
func (ctrl *ProvisionController) syncVolume(obj interface{}) error {
	volume, ok := obj.(*v1.PersistentVolume)
	if !ok {
		return fmt.Errorf("expected volume but got %+v", obj)
	}

	if ctrl.shouldDelete(volume) {
		startTime := time.Now()
		err := ctrl.deleteVolumeOperation(volume)
		ctrl.updateDeleteStats(volume, err, startTime)
		return err
	}
	return nil
}

// removeRecord returns a claim with its leader election record annotation and
// ResourceVersion set blank
func (ctrl *ProvisionController) removeRecord(claim *v1.PersistentVolumeClaim) (*v1.PersistentVolumeClaim, error) {
	claimClone := claim.DeepCopy()
	if claimClone.Annotations == nil {
		claimClone.Annotations = make(map[string]string)
	}
	claimClone.Annotations[rl.LeaderElectionRecordAnnotationKey] = ""

	claimClone.ResourceVersion = ""

	return claimClone, nil
}

// shouldProvision returns whether a claim should have a volume provisioned for
// it, i.e. whether a Provision is "desired"
func (ctrl *ProvisionController) shouldProvision(claim *v1.PersistentVolumeClaim) bool {
	if claim.Spec.VolumeName != "" {
		return false
	}

	if qualifier, ok := ctrl.provisioner.(Qualifier); ok {
		if !qualifier.ShouldProvision(claim) {
			return false
		}
	}

	// Kubernetes 1.5 provisioning with annStorageProvisioner
	if ctrl.kubeVersion.AtLeast(utilversion.MustParseSemantic("v1.5.0")) {
		if provisioner, found := claim.Annotations[annStorageProvisioner]; found {
			if provisioner == ctrl.provisionerName {
				return true
			}
			return false
		}
	} else {
		// Kubernetes 1.4 provisioning, evaluating class.Provisioner
		claimClass := helper.GetPersistentVolumeClaimClass(claim)
		provisioner, _, err := ctrl.getStorageClassFields(claimClass)
		if err != nil {
			glog.Errorf("Error getting claim %q's StorageClass's fields: %v", claimToClaimKey(claim), err)
			return false
		}
		if provisioner != ctrl.provisionerName {
			return false
		}

		return true
	}

	return false
}

// shouldDelete returns whether a volume should have its backing volume
// deleted, i.e. whether a Delete is "desired"
func (ctrl *ProvisionController) shouldDelete(volume *v1.PersistentVolume) bool {
	// In 1.5+ we delete only if the volume is in state Released. In 1.4 we must
	// delete if the volume is in state Failed too.
	if ctrl.kubeVersion.AtLeast(utilversion.MustParseSemantic("v1.5.0")) {
		if volume.Status.Phase != v1.VolumeReleased {
			return false
		}
	} else {
		if volume.Status.Phase != v1.VolumeReleased && volume.Status.Phase != v1.VolumeFailed {
			return false
		}
	}

	if volume.Spec.PersistentVolumeReclaimPolicy != v1.PersistentVolumeReclaimDelete {
		return false
	}

	if !metav1.HasAnnotation(volume.ObjectMeta, annDynamicallyProvisioned) {
		return false
	}

	if ann := volume.Annotations[annDynamicallyProvisioned]; ann != ctrl.provisionerName {
		return false
	}

	return true
}

// canProvision returns error if provisioner can't provision claim.
func (ctrl *ProvisionController) canProvision(claim *v1.PersistentVolumeClaim) error {
	// Check if this provisioner supports Block volume
	if util.CheckPersistentVolumeClaimModeBlock(claim) && !ctrl.supportsBlock() {
		return fmt.Errorf("%s does not support block volume provisioning", ctrl.provisionerName)
	}

	return nil
}

// lockProvisionClaimOperation wraps provisionClaimOperation. In case other
// controllers are serving the same claims, to prevent them all from creating
// volumes for a claim & racing to submit their PV, each controller creates a
// LeaderElector to instead race for the leadership (lock), where only the
// leader is tasked with provisioning & may try to do so. Returns error, which
// indicates whether provisioning should be retried (requeue the claim) or not
func (ctrl *ProvisionController) lockProvisionClaimOperation(claim *v1.PersistentVolumeClaim) error {
	rl := rl.ProvisionPVCLock{
		PVCMeta: claim.ObjectMeta,
		Client:  ctrl.client,
		LockConfig: rl.Config{
			Identity:      string(ctrl.identity),
			EventRecorder: ctrl.eventRecorder,
		},
	}
	var provisionErr error
	le, err := leaderelection.NewLeaderElector(leaderelection.Config{
		Lock:          &rl,
		LeaseDuration: ctrl.leaseDuration,
		RenewDeadline: ctrl.renewDeadline,
		RetryPeriod:   ctrl.retryPeriod,
		TermLimit:     ctrl.termLimit,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ <-chan struct{}) {
				startTime := time.Now()
				provisionErr = ctrl.provisionClaimOperation(claim)
				ctrl.updateProvisionStats(claim, provisionErr, startTime)
			},
			OnStoppedLeading: func() {
			},
		},
	})
	if err != nil {
		glog.Errorf("Error creating LeaderElector, can't provision for claim %q: %v", claimToClaimKey(claim), err)
		return err
	}

	ctrl.leaderElectorsMutex.Lock()
	ctrl.leaderElectors[claim.UID] = le
	ctrl.leaderElectorsMutex.Unlock()

	// To determine when to stop trying to acquire/renew the lock, watch for
	// provisioning success/failure. (The leader could get the result of its
	// operation but it has to watch anyway)
	stopCh := make(chan struct{})
	successCh, err := ctrl.watchProvisioning(claim, stopCh)
	if err != nil {
		glog.Errorf("Error watching for provisioning success, can't provision for claim %q: %v", claimToClaimKey(claim), err)
		return err
	}

	le.Run(successCh)

	close(stopCh)

	ctrl.leaderElectorsMutex.Lock()
	delete(ctrl.leaderElectors, claim.UID)
	ctrl.leaderElectorsMutex.Unlock()

	return provisionErr
}

func (ctrl *ProvisionController) updateProvisionStats(claim *v1.PersistentVolumeClaim, err error, startTime time.Time) {
	class := ""
	if claim.Spec.StorageClassName != nil {
		class = *claim.Spec.StorageClassName
	}
	if err != nil {
		metrics.PersistentVolumeClaimProvisionFailedTotal.WithLabelValues(class).Inc()
	} else {
		metrics.PersistentVolumeClaimProvisionDurationSeconds.WithLabelValues(class).Observe(time.Since(startTime).Seconds())
		metrics.PersistentVolumeClaimProvisionTotal.WithLabelValues(class).Inc()
	}
}

func (ctrl *ProvisionController) updateDeleteStats(volume *v1.PersistentVolume, err error, startTime time.Time) {
	class := volume.Spec.StorageClassName
	if err != nil {
		metrics.PersistentVolumeDeleteFailedTotal.WithLabelValues(class).Inc()
	} else {
		metrics.PersistentVolumeDeleteDurationSeconds.WithLabelValues(class).Observe(time.Since(startTime).Seconds())
		metrics.PersistentVolumeDeleteTotal.WithLabelValues(class).Inc()
	}
}

// provisionClaimOperation attempts to provision a volume for the given claim.
// Returns error, which indicates whether provisioning should be retried
// (requeue the claim) or not
func (ctrl *ProvisionController) provisionClaimOperation(claim *v1.PersistentVolumeClaim) error {
	// Most code here is identical to that found in controller.go of kube's PV controller...
	claimClass := helper.GetPersistentVolumeClaimClass(claim)
	glog.V(4).Infof("provisionClaimOperation [%s] started, class: %q", claimToClaimKey(claim), claimClass)

	//  A previous doProvisionClaim may just have finished while we were waiting for
	//  the locks. Check that PV (with deterministic name) hasn't been provisioned
	//  yet.
	pvName := ctrl.getProvisionedVolumeNameForClaim(claim)
	volume, err := ctrl.client.CoreV1().PersistentVolumes().Get(pvName, metav1.GetOptions{})
	if err == nil && volume != nil {
		// Volume has been already provisioned, nothing to do.
		glog.V(4).Infof("provisionClaimOperation [%s]: volume already exists, skipping", claimToClaimKey(claim))
		return nil
	}

	// Prepare a claimRef to the claim early (to fail before a volume is
	// provisioned)
	claimRef, err := ref.GetReference(scheme.Scheme, claim)
	if err != nil {
		glog.Errorf("Unexpected error getting claim reference to claim %q: %v", claimToClaimKey(claim), err)
		return nil
	}

	provisioner, parameters, err := ctrl.getStorageClassFields(claimClass)
	if err != nil {
		glog.Errorf("Error getting claim %q's StorageClass's fields: %v", claimToClaimKey(claim), err)
		return nil
	}
	if provisioner != ctrl.provisionerName {
		// class.Provisioner has either changed since shouldProvision() or
		// annDynamicallyProvisioned contains different provisioner than
		// class.Provisioner.
		glog.Errorf("Unknown provisioner %q requested in claim %q's StorageClass %q", provisioner, claimToClaimKey(claim), claimClass)
		return nil
	}

	// Check if this provisioner can provision this claim.
	if err := ctrl.canProvision(claim); err != nil {
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", err.Error())
		glog.Errorf("Failed to provision volume for claim %q with StorageClass %q: %v",
			claimToClaimKey(claim), claimClass, err)
		return nil
	}

	reclaimPolicy := v1.PersistentVolumeReclaimDelete
	if ctrl.kubeVersion.AtLeast(utilversion.MustParseSemantic("v1.8.0")) {
		reclaimPolicy, err = ctrl.fetchReclaimPolicy(claimClass)
		if err != nil {
			return err
		}
	}

	mountOptions, err := ctrl.fetchMountOptions(claimClass)
	if err != nil {
		return err
	}

	options := VolumeOptions{
		PersistentVolumeReclaimPolicy: reclaimPolicy,
		PVName:       pvName,
		PVC:          claim,
		MountOptions: mountOptions,
		Parameters:   parameters,
	}

	ctrl.eventRecorder.Event(claim, v1.EventTypeNormal, "Provisioning", fmt.Sprintf("External provisioner is provisioning volume for claim %q", claimToClaimKey(claim)))

	volume, err = ctrl.provisioner.Provision(options)
	if err != nil {
		if ierr, ok := err.(*IgnoredError); ok {
			// Provision ignored, do nothing and hope another provisioner will provision it.
			glog.Infof("provision of claim %q ignored: %v", claimToClaimKey(claim), ierr)
			return nil
		}
		strerr := fmt.Sprintf("Failed to provision volume with StorageClass %q: %v", claimClass, err)
		glog.Errorf("Failed to provision volume for claim %q with StorageClass %q: %v", claimToClaimKey(claim), claimClass, err)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)
		return err
	}

	glog.Infof("volume %q for claim %q created", volume.Name, claimToClaimKey(claim))

	// Set ClaimRef and the PV controller will bind and set annBoundByController for us
	volume.Spec.ClaimRef = claimRef

	metav1.SetMetaDataAnnotation(&volume.ObjectMeta, annDynamicallyProvisioned, ctrl.provisionerName)
	if ctrl.kubeVersion.AtLeast(utilversion.MustParseSemantic("v1.6.0")) {
		volume.Spec.StorageClassName = claimClass
	} else {
		metav1.SetMetaDataAnnotation(&volume.ObjectMeta, annClass, claimClass)
	}

	// Try to create the PV object several times
	for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
		glog.V(4).Infof("provisionClaimOperation [%s]: trying to save volume %s", claimToClaimKey(claim), volume.Name)
		if _, err = ctrl.client.CoreV1().PersistentVolumes().Create(volume); err == nil {
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
		glog.Error(strerr)
		ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningFailed", strerr)

		for i := 0; i < ctrl.createProvisionedPVRetryCount; i++ {
			if err = ctrl.provisioner.Delete(volume); err == nil {
				// Delete succeeded
				glog.V(4).Infof("provisionClaimOperation [%s]: cleaning volume %s succeeded", claimToClaimKey(claim), volume.Name)
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
			glog.Error(strerr)
			ctrl.eventRecorder.Event(claim, v1.EventTypeWarning, "ProvisioningCleanupFailed", strerr)
		}
	} else {
		glog.Infof("volume %q provisioned for claim %q", volume.Name, claimToClaimKey(claim))
		msg := fmt.Sprintf("Successfully provisioned volume %s", volume.Name)
		ctrl.eventRecorder.Event(claim, v1.EventTypeNormal, "ProvisioningSucceeded", msg)
	}

	return nil
}

// watchProvisioning returns a channel to which it sends the results of all
// provisioning attempts for the given claim. The PVC being modified to no
// longer need provisioning is considered a success.
func (ctrl *ProvisionController) watchProvisioning(claim *v1.PersistentVolumeClaim, stopChannel chan struct{}) (<-chan bool, error) {
	stopWatchPVC := make(chan struct{})
	pvcCh, err := ctrl.watchPVC(claim, stopWatchPVC)
	if err != nil {
		glog.Infof("cannot start watcher for PVC %s/%s: %v", claim.Namespace, claim.Name, err)
		return nil, err
	}

	successCh := make(chan bool, 0)

	go func() {
		defer close(stopWatchPVC)
		defer close(successCh)

		for {
			select {
			case _ = <-stopChannel:
				return

			case event := <-pvcCh:
				switch event.Object.(type) {
				case *v1.PersistentVolumeClaim:
					// PVC changed
					claim := event.Object.(*v1.PersistentVolumeClaim)
					glog.V(4).Infof("claim update received: %s %s/%s %s", event.Type, claim.Namespace, claim.Name, claim.Status.Phase)
					switch event.Type {
					case watch.Added, watch.Modified:
						if claim.Spec.VolumeName != "" {
							successCh <- true
						} else if !ctrl.shouldProvision(claim) {
							glog.Infof("claim %s/%s was modified to not ask for this provisioner", claim.Namespace, claim.Name)
							successCh <- true
						}

					case watch.Deleted:
						glog.Infof("claim %s/%s was deleted", claim.Namespace, claim.Name)
						successCh <- true

					case watch.Error:
						glog.Infof("claim %s/%s watcher failed", claim.Namespace, claim.Name)
						successCh <- true
					default:
					}
				case *v1.Event:
					// Event received
					claimEvent := event.Object.(*v1.Event)
					glog.V(4).Infof("claim event received: %s %s/%s %s/%s %s", event.Type, claimEvent.Namespace, claimEvent.Name, claimEvent.InvolvedObject.Namespace, claimEvent.InvolvedObject.Name, claimEvent.Reason)
					if claimEvent.Reason == "ProvisioningSucceeded" {
						successCh <- true
					} else if claimEvent.Reason == "ProvisioningFailed" {
						successCh <- false
					}
				}
			}
		}
	}()

	return successCh, nil
}

// watchPVC returns a watch on the given PVC and ProvisioningFailed &
// ProvisioningSucceeded events involving it
func (ctrl *ProvisionController) watchPVC(claim *v1.PersistentVolumeClaim, stopChannel chan struct{}) (<-chan watch.Event, error) {
	options := metav1.ListOptions{
		FieldSelector:   "metadata.name=" + claim.Name,
		Watch:           true,
		ResourceVersion: claim.ResourceVersion,
	}

	pvcWatch, err := ctrl.claimSource.Watch(options)
	if err != nil {
		return nil, err
	}

	failWatch, err := ctrl.getPVCEventWatch(claim, v1.EventTypeWarning, "ProvisioningFailed")
	if err != nil {
		pvcWatch.Stop()
		return nil, err
	}

	successWatch, err := ctrl.getPVCEventWatch(claim, v1.EventTypeNormal, "ProvisioningSucceeded")
	if err != nil {
		failWatch.Stop()
		pvcWatch.Stop()
		return nil, err
	}

	eventCh := make(chan watch.Event, 0)

	go func() {
		defer successWatch.Stop()
		defer failWatch.Stop()
		defer pvcWatch.Stop()
		defer close(eventCh)

		for {
			select {
			case _ = <-stopChannel:
				return

			case pvcEvent, ok := <-pvcWatch.ResultChan():
				if !ok {
					return
				}
				eventCh <- pvcEvent

			case failEvent, ok := <-failWatch.ResultChan():
				if !ok {
					return
				}
				eventCh <- failEvent

			case successEvent, ok := <-successWatch.ResultChan():
				if !ok {
					return
				}
				eventCh <- successEvent
			}
		}
	}()

	return eventCh, nil
}

// getPVCEventWatch returns a watch on the given PVC for the given event from
// this point forward.
func (ctrl *ProvisionController) getPVCEventWatch(claim *v1.PersistentVolumeClaim, eventType, reason string) (watch.Interface, error) {
	claimKind := "PersistentVolumeClaim"
	claimUID := string(claim.UID)
	fieldSelector := ctrl.client.CoreV1().Events(claim.Namespace).GetFieldSelector(&claim.Name, &claim.Namespace, &claimKind, &claimUID).String() + ",type=" + eventType + ",reason=" + reason

	list, err := ctrl.client.CoreV1().Events(claim.Namespace).List(metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, err
	}

	resourceVersion := ""
	if len(list.Items) >= 1 {
		resourceVersion = list.Items[len(list.Items)-1].ResourceVersion
	}

	return ctrl.client.CoreV1().Events(claim.Namespace).Watch(metav1.ListOptions{
		FieldSelector:   fieldSelector,
		Watch:           true,
		ResourceVersion: resourceVersion,
	})
}

// deleteVolumeOperation attempts to delete the volume backing the given
// volume. Returns error, which indicates whether deletion should be retried
// (requeue the volume) or not
func (ctrl *ProvisionController) deleteVolumeOperation(volume *v1.PersistentVolume) error {
	glog.V(4).Infof("deleteVolumeOperation [%s] started", volume.Name)

	// This method may have been waiting for a volume lock for some time.
	// Our check does not have to be as sophisticated as PV controller's, we can
	// trust that the PV controller has set the PV to Released/Failed and it's
	// ours to delete
	newVolume, err := ctrl.client.CoreV1().PersistentVolumes().Get(volume.Name, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	if !ctrl.shouldDelete(newVolume) {
		glog.Infof("volume %q no longer needs deletion, skipping", volume.Name)
		return nil
	}

	err = ctrl.provisioner.Delete(volume)
	if err != nil {
		if ierr, ok := err.(*IgnoredError); ok {
			// Delete ignored, do nothing and hope another provisioner will delete it.
			glog.Infof("deletion of volume %q ignored: %v", volume.Name, ierr)
			return nil
		}
		// Delete failed, emit an event.
		glog.Errorf("Deletion of volume %q failed: %v", volume.Name, err)
		ctrl.eventRecorder.Event(volume, v1.EventTypeWarning, "VolumeFailedDelete", err.Error())
		return err
	}

	glog.Infof("volume %q deleted", volume.Name)

	glog.V(4).Infof("deleteVolumeOperation [%s]: success", volume.Name)
	// Delete the volume
	if err = ctrl.client.CoreV1().PersistentVolumes().Delete(volume.Name, nil); err != nil {
		// Oops, could not delete the volume and therefore the controller will
		// try to delete the volume again on next update.
		glog.Infof("failed to delete volume %q from database: %v", volume.Name, err)
		return err
	}

	glog.Infof("volume %q deleted from database", volume.Name)
	return nil
}

// getProvisionedVolumeNameForClaim returns PV.Name for the provisioned volume.
// The name must be unique.
func (ctrl *ProvisionController) getProvisionedVolumeNameForClaim(claim *v1.PersistentVolumeClaim) string {
	return "pvc-" + string(claim.UID)
}

func (ctrl *ProvisionController) getStorageClassFields(name string) (string, map[string]string, error) {
	classObj, found, err := ctrl.classes.GetByKey(name)
	if err != nil {
		return "", nil, err
	}
	if !found {
		return "", nil, fmt.Errorf("StorageClass %q not found", name)
		// 3. It tries to find a StorageClass instance referenced by annotation
		//    `claim.Annotations["volume.beta.kubernetes.io/storage-class"]`. If not
		//    found, it SHOULD report an error (by sending an event to the claim) and it
		//    SHOULD retry periodically with step i.
	}
	switch class := classObj.(type) {
	case *storage.StorageClass:
		return class.Provisioner, class.Parameters, nil
	case *storagebeta.StorageClass:
		return class.Provisioner, class.Parameters, nil
	}
	return "", nil, fmt.Errorf("Cannot convert object to StorageClass: %+v", classObj)
}

func claimToClaimKey(claim *v1.PersistentVolumeClaim) string {
	return fmt.Sprintf("%s/%s", claim.Namespace, claim.Name)
}

func (ctrl *ProvisionController) fetchReclaimPolicy(storageClassName string) (v1.PersistentVolumeReclaimPolicy, error) {
	classObj, found, err := ctrl.classes.GetByKey(storageClassName)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("StorageClass %q not found", storageClassName)
	}

	switch class := classObj.(type) {
	case *storage.StorageClass:
		return *class.ReclaimPolicy, nil
	case *storagebeta.StorageClass:
		return *class.ReclaimPolicy, nil
	}

	return v1.PersistentVolumeReclaimDelete, fmt.Errorf("Cannot convert object to StorageClass: %+v", classObj)
}

func (ctrl *ProvisionController) fetchMountOptions(storageClassName string) ([]string, error) {
	classObj, found, err := ctrl.classes.GetByKey(storageClassName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("StorageClass %q not found", storageClassName)
	}

	switch class := classObj.(type) {
	case *storage.StorageClass:
		return class.MountOptions, nil
	case *storagebeta.StorageClass:
		return class.MountOptions, nil
	}

	return nil, fmt.Errorf("Cannot convert object to StorageClass: %+v", classObj)
}

// supportsBlock returns whether a provisioner supports block volume.
// Provisioners that implement BlockProvisioner interface and return true to SupportsBlock
// will be regarded as supported for block volume.
func (ctrl *ProvisionController) supportsBlock() bool {
	if blockProvisioner, ok := ctrl.provisioner.(BlockProvisioner); ok {
		return blockProvisioner.SupportsBlock()
	}
	return false
}
