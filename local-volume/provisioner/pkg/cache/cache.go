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

	"k8s.io/client-go/pkg/api/v1"
)

type VolumeCache struct {
	mutex sync.Mutex
	pvs   map[string]*v1.PersistentVolume
}

func NewVolumeCache() *VolumeCache {
	cache := &VolumeCache{pvs: map[string]*v1.PersistentVolume{}}
	return cache
}

func (cache *VolumeCache) PVExists(pvName string) bool {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	_, exists := cache.pvs[pvName]
	return exists
}

func (cache *VolumeCache) AddPV(pv *v1.PersistentVolume) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.pvs[pv.Name] = pv
	glog.Infof("Added pv %q to cache", pv.Name)
}

func (cache *VolumeCache) UpdatePV(pv *v1.PersistentVolume) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	cache.pvs[pv.Name] = pv
	glog.Infof("Updated pv %q to cache", pv.Name)
}

func (cache *VolumeCache) DeletePV(pvName string) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	delete(cache.pvs, pvName)
	glog.Infof("Deleted pv %q from cache", pvName)
}

func (cache *VolumeCache) ListPVs() []*v1.PersistentVolume {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	pvs := []*v1.PersistentVolume{}
	for _, pv := range cache.pvs {
		pvs = append(pvs, pv)
	}
	return pvs
}
