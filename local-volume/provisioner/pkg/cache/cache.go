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

package cache

import (
	"sync"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
)

// VolumeCache keeps all the PersistentVolumes that have been created by this provisioner.
// It is periodically updated by the Populator.
// The Deleter and Discoverer use the VolumeCache to check on created PVs
type VolumeCache struct {
	mutex sync.Mutex
	pvs   map[string]*v1.PersistentVolume
}

// NewVolumeCache creates a new PV cache object for storing PVs created by this provisioner.
func NewVolumeCache() *VolumeCache {
	return &VolumeCache{pvs: map[string]*v1.PersistentVolume{}}
}

// GetPV returns the PV object given the PV name
func (cache *VolumeCache) GetPV(pvName string) (*v1.PersistentVolume, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	pv, exists := cache.pvs[pvName]
	return pv, exists
}

// AddPV adds the PV object to the cache
func (cache *VolumeCache) AddPV(pv *v1.PersistentVolume) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.pvs[pv.Name] = pv
	glog.Infof("Added pv %q to cache", pv.Name)
}

// UpdatePV updates the PV object in the cache
func (cache *VolumeCache) UpdatePV(pv *v1.PersistentVolume) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.pvs[pv.Name] = pv
	glog.Infof("Updated pv %q to cache", pv.Name)
}

// DeletePV deletes the PV object from the cache
func (cache *VolumeCache) DeletePV(pvName string) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	delete(cache.pvs, pvName)
	glog.Infof("Deleted pv %q from cache", pvName)
}

// ListPVs returns a list of all the PVs in the cache
func (cache *VolumeCache) ListPVs() []*v1.PersistentVolume {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	pvs := []*v1.PersistentVolume{}
	for _, pv := range cache.pvs {
		pvs = append(pvs, pv)
	}
	return pvs
}
