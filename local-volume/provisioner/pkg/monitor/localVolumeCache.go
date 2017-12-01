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

package monitor

import (
	"sync"

	"k8s.io/api/core/v1"
)

// LocalVolumeMap is the interface to store local volumes
type LocalVolumeMap interface {
	AddLocalVolume(pv *v1.PersistentVolume)

	UpdateLocalVolume(newPV *v1.PersistentVolume)

	DeleteLocalVolume(pv *v1.PersistentVolume)

	GetPVs() []*v1.PersistentVolume
}

type localVolumeMap struct {
	// for guarding access to pvs map
	sync.RWMutex

	// local storage PV map of unique pv name and pv obj
	volumeMap map[string]*v1.PersistentVolume
}

// NewLocalVolumeMap returns new LocalVolumeMap which acts as a cache
// for holding local storage PVs.
func NewLocalVolumeMap() LocalVolumeMap {
	localVolumeMap := &localVolumeMap{}
	localVolumeMap.volumeMap = make(map[string]*v1.PersistentVolume)
	return localVolumeMap
}

// TODO: just add local storage PVs which belongs to the specific node
func (lvm *localVolumeMap) AddLocalVolume(pv *v1.PersistentVolume) {
	lvm.Lock()
	defer lvm.Unlock()

	lvm.volumeMap[pv.Name] = pv
}

func (lvm *localVolumeMap) UpdateLocalVolume(newPV *v1.PersistentVolume) {
	lvm.Lock()
	defer lvm.Unlock()

	lvm.volumeMap[newPV.Name] = newPV
}

func (lvm *localVolumeMap) DeleteLocalVolume(pv *v1.PersistentVolume) {
	lvm.Lock()
	defer lvm.Unlock()

	delete(lvm.volumeMap, pv.Name)
}

func (lvm *localVolumeMap) GetPVs() []*v1.PersistentVolume {
	lvm.Lock()
	defer lvm.Unlock()

	pvs := []*v1.PersistentVolume{}
	for _, pv := range lvm.volumeMap {
		pvs = append(pvs, pv)
	}

	return pvs
}
