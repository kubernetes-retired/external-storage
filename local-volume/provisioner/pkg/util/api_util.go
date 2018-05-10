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

package util

import (
	"fmt"
	"time"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics"

	batch_v1 "k8s.io/api/batch/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// APIUtil is an interface for the K8s API
type APIUtil interface {
	// Create PersistentVolume object
	CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error)

	// Delete PersistentVolume object
	DeletePV(pvName string) error

	// CreateJob Creates a Job execution.
	CreateJob(job *batch_v1.Job) error

	// DeleteJob deletes specified Job by its name and namespace.
	DeleteJob(jobName string, namespace string) error
}

var _ APIUtil = &apiUtil{}

type apiUtil struct {
	client *kubernetes.Clientset
}

// NewAPIUtil creates a new APIUtil object that represents the K8s API
func NewAPIUtil(client *kubernetes.Clientset) APIUtil {
	return &apiUtil{client: client}
}

// CreatePV will create a PersistentVolume
func (u *apiUtil) CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error) {
	startTime := time.Now()
	metrics.APIServerRequestsTotal.WithLabelValues(metrics.APIServerRequestCreate).Inc()
	pv, err := u.client.CoreV1().PersistentVolumes().Create(pv)
	metrics.APIServerRequestsDurationSeconds.WithLabelValues(metrics.APIServerRequestCreate).Observe(time.Since(startTime).Seconds())
	if err != nil {
		metrics.APIServerRequestsFailedTotal.WithLabelValues(metrics.APIServerRequestCreate).Inc()
	}
	return pv, err
}

// DeletePV will delete a PersistentVolume
func (u *apiUtil) DeletePV(pvName string) error {
	startTime := time.Now()
	metrics.APIServerRequestsTotal.WithLabelValues(metrics.APIServerRequestDelete).Inc()
	err := u.client.CoreV1().PersistentVolumes().Delete(pvName, &metav1.DeleteOptions{})
	metrics.APIServerRequestsDurationSeconds.WithLabelValues(metrics.APIServerRequestDelete).Observe(time.Since(startTime).Seconds())
	if err != nil {
		metrics.APIServerRequestsFailedTotal.WithLabelValues(metrics.APIServerRequestDelete).Inc()
	}
	return err
}

func (u *apiUtil) CreateJob(job *batch_v1.Job) error {
	_, err := u.client.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		return err
	}
	return nil
}

func (u *apiUtil) DeleteJob(jobName string, namespace string) error {
	deleteProp := metav1.DeletePropagationForeground
	if err := u.client.BatchV1().Jobs(namespace).Delete(jobName, &metav1.DeleteOptions{PropagationPolicy: &deleteProp}); err != nil {
		return err
	}

	return nil
}

var _ APIUtil = &FakeAPIUtil{}

// FakeAPIUtil is a fake API wrapper for unit testing
type FakeAPIUtil struct {
	createdPVs  map[string]*v1.PersistentVolume
	deletedPVs  map[string]*v1.PersistentVolume
	CreatedJobs map[string]*batch_v1.Job
	DeletedJobs map[string]string
	shouldFail  bool
	cache       *cache.VolumeCache
}

// NewFakeAPIUtil returns an APIUtil object that can be used for unit testing
func NewFakeAPIUtil(shouldFail bool, cache *cache.VolumeCache) *FakeAPIUtil {
	return &FakeAPIUtil{
		createdPVs:  map[string]*v1.PersistentVolume{},
		deletedPVs:  map[string]*v1.PersistentVolume{},
		CreatedJobs: map[string]*batch_v1.Job{},
		DeletedJobs: map[string]string{},
		shouldFail:  shouldFail,
		cache:       cache,
	}
}

// CreatePV will add the PV to the created list and cache
func (u *FakeAPIUtil) CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error) {
	if u.shouldFail {
		return nil, fmt.Errorf("API failed")
	}

	u.createdPVs[pv.Name] = pv
	u.cache.AddPV(pv)
	return pv, nil
}

// DeletePV will delete the PV from the created list and cache, and also add it to the deleted list
func (u *FakeAPIUtil) DeletePV(pvName string) error {
	if u.shouldFail {
		return fmt.Errorf("API failed")
	}

	pv, exists := u.cache.GetPV(pvName)
	if exists {
		u.deletedPVs[pvName] = pv
		delete(u.createdPVs, pvName)
		u.cache.DeletePV(pvName)
		return nil
	}
	return errors.NewNotFound(v1.Resource("persistentvolumes"), pvName)
}

// GetAndResetCreatedPVs returns createdPVs and resets the map
// This is only for testing
func (u *FakeAPIUtil) GetAndResetCreatedPVs() map[string]*v1.PersistentVolume {
	createdPVs := u.createdPVs
	u.createdPVs = map[string]*v1.PersistentVolume{}
	return createdPVs
}

// GetAndResetDeletedPVs returns createdPVs and resets the map
// This is only for testing
func (u *FakeAPIUtil) GetAndResetDeletedPVs() map[string]*v1.PersistentVolume {
	deletedPVs := u.deletedPVs
	u.deletedPVs = map[string]*v1.PersistentVolume{}
	return deletedPVs
}

// CreateJob mocks job create method.
func (u *FakeAPIUtil) CreateJob(job *batch_v1.Job) error {
	u.CreatedJobs[job.Namespace+"/"+job.Name] = job
	return nil
}

// DeleteJob mocks delete jon method.
func (u *FakeAPIUtil) DeleteJob(jobName string, namespace string) error {
	u.DeletedJobs[namespace+"/"+jobName] = jobName
	return nil
}
