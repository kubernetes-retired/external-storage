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

type LvmBackend struct {
	volumeGroup string
	rootPath    string
}

func NewLvmBackend(volumeGroup, rootPath string) *LvmBackend {
	return &LvmBackend{
		volumeGroup: volumeGroup,
		rootPath:    rootPath,
	}
}

func (w *LvmBackend) CreateLocalVolume(volReq *LocalVolumeReq) (*LocalVolumeInfo, error) {
	_, err := createLv(volReq.VolumeName, volReq.SizeGB, w.volumeGroup)
	if err != nil {
		return nil, err
	}
	return &LocalVolumeInfo{
		VolumeName: volReq.VolumeName,
		SizeGB:     volReq.SizeGB,
		VolumePath: getLvPath(w.rootPath, w.volumeGroup, volReq.VolumeName),
	}, nil
}

func (w *LvmBackend) DeleteLocalVolume(volName string) error {
	_, err := deleteLv(w.rootPath, w.volumeGroup, volName)
	return err
}

func (w *LvmBackend) GetCapacity() (int64, error) {
	return getVgSize(w.volumeGroup)
}
