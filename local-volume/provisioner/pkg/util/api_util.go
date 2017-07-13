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

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// APIUtil is an interface for the K8s API
type APIUtil interface {
	// Create PersistentVolume object
	CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error)

	// Delete PersistentVolume object
	DeletePV(pvName string) error
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
	return u.client.Core().PersistentVolumes().Create(pv)
}

// DeletePV will delete a PersistentVolume
func (u *apiUtil) DeletePV(pvName string) error {
	return u.client.Core().PersistentVolumes().Delete(pvName, &metav1.DeleteOptions{})
}

var _ APIUtil = &FakeAPIUtil{}

// FakeAPIUtil is a fake API wrapper for unit testing
type FakeAPIUtil struct {
	createdPVs map[string]*v1.PersistentVolume
	deletedPVs map[string]*v1.PersistentVolume
	shouldFail bool
	cache      *cache.VolumeCache
}

// NewFakeAPIUtil returns an APIUtil object that can be used for unit testing
func NewFakeAPIUtil(shouldFail bool, cache *cache.VolumeCache) *FakeAPIUtil {
	return &FakeAPIUtil{
		createdPVs: map[string]*v1.PersistentVolume{},
		deletedPVs: map[string]*v1.PersistentVolume{},
		shouldFail: shouldFail,
		cache:      cache,
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
	}
	return nil
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
