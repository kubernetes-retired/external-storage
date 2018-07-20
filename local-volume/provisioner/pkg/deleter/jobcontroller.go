/*
Copyright 2018 The Kubernetes Authors.

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

package deleter

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	batch_v1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	maxRetries = 10
	// JobContainerName is name of the container running the cleanup process.
	JobContainerName = "cleaner"
	// JobNamePrefix is the prefix of the name of the cleaning job.
	JobNamePrefix = "cleanup-"
	// PVLabel is the label name whose value is the pv name.
	PVLabel = "pv"
	// PVUuidLabel  is the label name whose value is the pv uuid.
	PVUuidLabel = "pvuuid"
	// DeviceAnnotation is the annotation that specifies the device path.
	DeviceAnnotation = "device"
	// StartTimeAnnotation is the annotation that specifies the job start time.
	// This is the time when we begin to submit job to apiserver. We use this
	// instead of job or pod start time to include k8s pod start latency into
	// volume deletion time.
	// Time is formatted in time.RFC3339Nano.
	StartTimeAnnotation = "start-time"
)

// JobController defines the interface for the job controller.
type JobController interface {
	Run(stopCh <-chan struct{})
	IsCleaningJobRunning(pvName string) bool
	RemoveJob(pvName string) (CleanupState, *time.Time, error)
}

var _ JobController = &jobController{}

type jobController struct {
	*common.RuntimeConfig
	namespace string
	queue     workqueue.RateLimitingInterface
	jobLister batchlisters.JobLister
}

// NewJobController instantiates  a new job controller.
func NewJobController(labelmap map[string]string, config *common.RuntimeConfig) (JobController, error) {
	namespace := config.Namespace
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	labelset := labels.Set(labelmap)
	optionsModifier := func(options *meta_v1.ListOptions) {
		options.LabelSelector = labels.SelectorFromSet(labelset).String()
	}

	informer := config.InformerFactory.InformerFor(&batch_v1.Job{}, func(client kubernetes.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		return cache.NewSharedIndexInformer(
			cache.NewFilteredListWatchFromClient(client.BatchV1().RESTClient(), "jobs", namespace, optionsModifier),
			&batch_v1.Job{},
			resyncPeriod,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		)
	})

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
			return
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err == nil {
				glog.Infof("Got update notification for %s", key)
				queue.Add(key)
			}
			return
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				glog.Infof("Got delete notification for %s", key)
				queue.Add(key)
			}
		},
	})

	return &jobController{
		RuntimeConfig: config,
		namespace:     namespace,
		queue:         queue,
		jobLister:     batchlisters.NewJobLister(informer.GetIndexer()),
	}, nil

}

func (c *jobController) Run(stopCh <-chan struct{}) {

	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	glog.Infof("Starting Job controller")
	defer glog.Infof("Shutting down Job controller")

	// runWorker will loop until "something bad" happens.  The .Until will
	// then rekick the worker after one second
	wait.Until(c.runWorker, time.Second, stopCh)
}

func (c *jobController) runWorker() {
	for c.processNextItem() {
	}
}

// processNextWorkItem serially handles the events provided by the informer.
func (c *jobController) processNextItem() bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}

	defer c.queue.Done(key)

	err := c.processItem(key.(string))
	if err == nil {
		// No error, tell the queue to stop tracking history
		c.queue.Forget(key)
	} else if c.queue.NumRequeues(key) < maxRetries {
		glog.Errorf("Error processing %s (will retry): %v", key, err)
		// requeue the item to work on later
		c.queue.AddRateLimited(key)
	} else {
		// err != nil and too many retries
		glog.Errorf("Error processing %s (giving up): %v", key, err)
		c.queue.Forget(key)
		utilruntime.HandleError(err)
	}

	return true
}

func (c *jobController) processItem(key string) error {
	glog.Infof("Processing change to Pod %s", key)
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	job, err := c.jobLister.Jobs(namespace).Get(name)
	if errors.IsNotFound(err) {
		glog.Infof("Job %s has been deleted", key)
		return nil
	}
	if err != nil {
		return fmt.Errorf("Error fetching object with key %s from store: %v", key, err)
	}

	if job.DeletionTimestamp != nil {
		// This is just an event in response to the deletion of the job. Job deletion is probably still in progress.
		glog.Infof("Job %s deletion timestamp has been set (%s)", key,
			*job.DeletionTimestamp)
		return nil
	}

	if job.Status.Succeeded == 0 {
		glog.Infof("Job %s has not yet completed successfully", key)
		return nil
	}

	glog.Infof("Job %s has completed successfully", key)
	return nil
}

// IsCleaningJobRunning returns true if a cleaning job is running for the specified PV.
func (c *jobController) IsCleaningJobRunning(pvName string) bool {
	jobName := generateCleaningJobName(pvName)
	job, err := c.jobLister.Jobs(c.namespace).Get(jobName)
	if errors.IsNotFound(err) {
		return false
	}

	if err != nil {
		glog.Warningf("Failed to check whether job %s is running (%s). Assuming its still running.",
			jobName, err)
		return true
	}

	return job.Status.Succeeded <= 0
}

// RemoveJob returns true and deletes the job if the cleaning job has completed.
func (c *jobController) RemoveJob(pvName string) (CleanupState, *time.Time, error) {
	jobName := generateCleaningJobName(pvName)
	job, err := c.jobLister.Jobs(c.namespace).Get(jobName)
	if err != nil {
		if errors.IsNotFound(err) {
			return CSNotFound, nil, nil
		}
		return CSUnknown, nil, fmt.Errorf("Failed to check whether job %s has succeeded. Error - %s",
			jobName, err.Error())
	}

	var startTime *time.Time
	if startTimeStr, ok := job.Annotations[StartTimeAnnotation]; ok {
		parsedStartTime, err := time.Parse(time.RFC3339Nano, startTimeStr)
		if err == nil {
			startTime = &parsedStartTime
		} else {
			glog.Errorf("Failed to parse start time %s: %v", startTimeStr, err)
		}
	}

	if job.Status.Succeeded == 0 {
		// Jobs has not yet succeeded. We assume failed jobs to be still running, until addressed by admin.
		return CSUnknown, nil, fmt.Errorf("Error deleting Job %q: Cannot remove job that has not succeeded", job.Name)
	}

	if err := c.RuntimeConfig.APIUtil.DeleteJob(job.Name, c.namespace); err != nil {
		return CSUnknown, nil, fmt.Errorf("Error deleting Job %q: %s", job.Name, err.Error())
	}

	return CSSucceeded, startTime, nil
}

// NewCleanupJob creates manifest for a cleaning job.
func NewCleanupJob(pv *apiv1.PersistentVolume, volMode apiv1.PersistentVolumeMode, imageName string, nodeName string, namespace string, mountPath string,
	config common.MountConfig) (*batch_v1.Job, error) {
	priv := true
	// Container definition
	jobContainer := apiv1.Container{
		Name:  JobContainerName,
		Image: imageName,
		SecurityContext: &apiv1.SecurityContext{
			Privileged: &priv,
		},
	}
	if volMode == apiv1.PersistentVolumeBlock {
		jobContainer.Command = config.BlockCleanerCommand
		jobContainer.Env = []apiv1.EnvVar{{Name: common.LocalPVEnv, Value: mountPath}}
	} else if volMode == apiv1.PersistentVolumeFilesystem {
		// We only have one way to clean filesystem, so no need to customize
		// filesystem cleaner command.
		jobContainer.Command = []string{"/scripts/fsclean.sh"}
		jobContainer.Env = []apiv1.EnvVar{{Name: common.LocalFilesystemEnv, Value: mountPath}}
	} else {
		return nil, fmt.Errorf("unknown PersistentVolume mode: %v", volMode)
	}
	mountName := common.GenerateMountName(&config)
	volumes := []apiv1.Volume{
		{
			Name: mountName,
			VolumeSource: apiv1.VolumeSource{
				HostPath: &apiv1.HostPathVolumeSource{
					Path: config.HostDir,
				},
			},
		},
	}
	jobContainer.VolumeMounts = []apiv1.VolumeMount{{
		Name:      mountName,
		MountPath: config.MountDir},
	}

	// Make job query-able by some useful labels for admins.
	labels := map[string]string{
		common.NodeNameLabel: nodeName,
		PVLabel:              pv.Name,
		PVUuidLabel:          string(pv.UID),
	}

	// Annotate job with useful information that cannot be set as labels due to label name restrictions.
	annotations := map[string]string{
		DeviceAnnotation:    mountPath,
		StartTimeAnnotation: time.Now().Format(time.RFC3339Nano),
	}

	podTemplate := apiv1.Pod{}
	podTemplate.Spec = apiv1.PodSpec{
		Containers:   []apiv1.Container{jobContainer},
		Volumes:      volumes,
		NodeSelector: map[string]string{common.NodeNameLabel: nodeName},
	}
	podTemplate.ObjectMeta = meta_v1.ObjectMeta{
		Name:        generateCleaningJobName(pv.Name),
		Namespace:   namespace,
		Labels:      labels,
		Annotations: annotations,
	}
	job := &batch_v1.Job{}
	job.ObjectMeta = podTemplate.ObjectMeta
	job.Spec.Template.Spec = podTemplate.Spec
	job.Spec.Template.Spec.RestartPolicy = apiv1.RestartPolicyOnFailure

	return job, nil
}

func generateCleaningJobName(pvName string) string {
	return JobNamePrefix + pvName
}

var _ JobController = &FakeJobController{}

// FakeJobController for mocking.
type FakeJobController struct {
	pvCleanupRunning map[string]CleanupState
	// IsRunningCount keeps count of number of times IsRunning() was called
	IsRunningCount       int
	RemoveCompletedCount int
}

// NewFakeJobController instantiates mock job controller.
func NewFakeJobController() *FakeJobController {
	return &FakeJobController{pvCleanupRunning: map[string]CleanupState{}}
}

// Run mocks the interface method.
func (c *FakeJobController) Run(stopCh <-chan struct{}) {
}

// MarkRunning simulates a job running for specified PV.
func (c *FakeJobController) MarkRunning(pvName string) {
	c.pvCleanupRunning[pvName] = CSRunning
}

// MarkSucceeded simulates a job running for specified PV.
func (c *FakeJobController) MarkSucceeded(pvName string) {
	c.pvCleanupRunning[pvName] = CSSucceeded
}

// IsCleaningJobRunning mocks the interface method.
func (c *FakeJobController) IsCleaningJobRunning(pvName string) bool {
	c.IsRunningCount++
	_, exists := c.pvCleanupRunning[pvName]
	return exists
}

// RemoveJob mocks the interface method.
func (c *FakeJobController) RemoveJob(pvName string) (CleanupState, *time.Time, error) {
	c.RemoveCompletedCount++
	status, exists := c.pvCleanupRunning[pvName]
	if !exists {
		return CSNotFound, nil, nil
	}
	if status != CSSucceeded {
		return CSUnknown, nil, fmt.Errorf("cannot remove job that has not yet completed %s status %d", pvName, status)
	}
	return CSSucceeded, nil, nil
}
