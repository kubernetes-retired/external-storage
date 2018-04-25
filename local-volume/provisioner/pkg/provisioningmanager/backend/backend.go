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

package backend

// Local Volume configuration Information
type LocalVolumeReq struct {
	// Volume name
	VolumeName string

	// Size in GiB
	SizeGB int
}

// Created Local Volume's detail Information
type LocalVolumeInfo struct {
	// Volume name
	VolumeName string

	// Size in GiB
	SizeGB int

	// Volume Path
	VolumePath string
}

// StorageBackend is used to de-couple local provisioning manager and backend storage.
type StorageBackend interface {
	CreateLocalVolume(volReq *LocalVolumeReq) (*LocalVolumeInfo, error)
	DeleteLocalVolume(volName string) error
	GetCapacity() (int64, error)
}

type FakeBackend struct {
	Capacity int64
	RootPath string
}

func NewFakeBackend(capacity int64, rootPath string) *FakeBackend {
	return &FakeBackend{
		Capacity: capacity,
		RootPath: rootPath,
	}
}

func (w *FakeBackend) CreateLocalVolume(volReq *LocalVolumeReq) (*LocalVolumeInfo, error) {
	return &LocalVolumeInfo{
		VolumeName: volReq.VolumeName,
		VolumePath: w.RootPath + "/" + volReq.VolumeName,
		SizeGB:     volReq.SizeGB,
	}, nil
}

func (w *FakeBackend) DeleteLocalVolume(volName string) error {
	return nil
}

func (w *FakeBackend) GetCapacity() (int64, error) {
	return w.Capacity, nil
}
