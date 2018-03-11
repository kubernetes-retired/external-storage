/*
Copyright 2015 The Kubernetes Authors.

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

package volume

import (
	"fmt"

	"k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
)

// GetClassForVolume returns StorageClass
func GetClassForVolume(kubeClient kubernetes.Interface, pv *v1.PersistentVolume) (*storage.StorageClass, error) {
	if kubeClient == nil {
		return nil, fmt.Errorf("Cannot get kube client")
	}
	className := helper.GetPersistentVolumeClass(pv)
	if className == "" {
		return nil, fmt.Errorf("Volume has no storage class")
	}

	class, err := kubeClient.StorageV1().StorageClasses().Get(className, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return class, nil
}
