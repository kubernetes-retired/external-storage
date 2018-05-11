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

package sharedfilesystems

import (
	"fmt"
	"strconv"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func getPVAccessMode(PVCAccessMode []v1.PersistentVolumeAccessMode) []v1.PersistentVolumeAccessMode {
	if len(PVCAccessMode) > 0 {
		return PVCAccessMode
	}
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
		v1.ReadOnlyMany,
		v1.ReadWriteMany,
	}
}

func getPVCStorageSize(pvc *v1.PersistentVolumeClaim) (int, error) {
	var storageSize resource.Quantity
	var ok bool
	errStorageSizeNotConfigured := fmt.Errorf("requested storage capacity must be set")
	if pvc.Spec.Resources.Requests == nil {
		return 0, errStorageSizeNotConfigured
	}
	if storageSize, ok = pvc.Spec.Resources.Requests[v1.ResourceStorage]; !ok {
		return 0, errStorageSizeNotConfigured
	}
	if storageSize.IsZero() {
		return 0, fmt.Errorf("requested storage size must not have zero value")
	}
	if storageSize.Sign() == -1 {
		return 0, fmt.Errorf("requested storage size must be greater than zero")
	}
	canonicalValue, _ := storageSize.AsScale(resource.Giga)
	var buf []byte
	storageSizeAsByte, _ := canonicalValue.AsCanonicalBytes(buf)
	var i int
	var err error
	if i, err = strconv.Atoi(string(storageSizeAsByte)); err != nil {
		return 0, fmt.Errorf("requested storage size is not a number")
	}
	return i, nil
}
