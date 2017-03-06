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

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/storage/v1beta1"
)

const (
	// StorageClassAnnotation represents the storage class associated with a resource.
	// It currently matches the Beta value and can change when official is set.
	// - in PersistentVolumeClaim it represents required class to match.
	//   Only PersistentVolumes with the same class (i.e. annotation with the same
	//   value) can be bound to the claim. In case no such volume exists, the
	//   controller will provision a new one using StorageClass instance with
	//   the same name as the annotation value.
	// - in PersistentVolume it represents storage class to which the persistent
	//   volume belongs.
	StorageClassAnnotation = "volume.beta.kubernetes.io/storage-class"

	// VolumeGidAnnotationKey is the key of the annotation on the PersistentVolume
	// object that specifies a supplemental GID.
	VolumeGidAnnotationKey = "pv.beta.kubernetes.io/gid"
)

// GetVolumeStorageClass returns value of StorageClassAnnotation or empty string in case
// the annotation does not exist.
// TODO: change to PersistentVolume.Spec.Class value when this attribute is
// introduced.
func GetVolumeStorageClass(volume *v1.PersistentVolume) string {
	if class, found := volume.Annotations[StorageClassAnnotation]; found {
		return class
	}

	// 'nil' is interpreted as "", i.e. the volume does not belong to any class.
	return ""
}

// GetClaimStorageClass returns name of class that is requested by given claim.
// Request for `nil` class is interpreted as request for class "",
// i.e. for a classless PV.
// TODO: change to PersistentVolumeClaim.Spec.Class value when this
// attribute is introduced.
func GetClaimStorageClass(claim *v1.PersistentVolumeClaim) string {
	if class, found := claim.Annotations[StorageClassAnnotation]; found {
		return class
	}

	return ""
}

// GetClassForVolume gets the volume's Storage Class
func GetClassForVolume(kubeClient kubernetes.Interface, pv *v1.PersistentVolume) (*v1beta1.StorageClass, error) {
	if kubeClient == nil {
		return nil, fmt.Errorf("Cannot get kube client")
	}
	// TODO: replace with a real attribute after beta
	className, found := pv.Annotations["volume.beta.kubernetes.io/storage-class"]
	if !found {
		return nil, fmt.Errorf("Volume has no class annotation")
	}

	class, err := kubeClient.Storage().StorageClasses().Get(className)
	if err != nil {
		return nil, err
	}
	return class, nil
}
