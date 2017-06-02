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

package common

import (
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	AnnProvisionedBy = "pv.kubernetes.io/provisioned-by"
	// hostname is not the best choice, but it's what pod and node affinity also use
	NodeLabelKey = metav1.LabelHostname
)

type UserConfig struct {
	// Node object for this node
	Node *v1.Node
	// The hostpath directory
	HostDir string
	// The mount point of the hostpath volume
	MountDir string
	// key = storageclass, value = relative directory to search in
	DiscoveryMap map[string]string
}

type RuntimeConfig struct {
	*UserConfig
	// Unique name of this provisioner
	Name string
	// K8s API client
	Client *kubernetes.Clientset
	// Cache to store PVs managed by this provisioner
	Cache *cache.VolumeCache
	// K8s API layer
	APIUtil util.APIUtil
	// Volume util layer
	VolUtil util.VolumeUtil
}

type LocalPVConfig struct {
	Name            string
	HostPath        string
	StorageClass    string
	ProvisionerName string
	AffinityAnn     string
}

func CreateLocalPVSpec(config *LocalPVConfig) *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.Name,
			Annotations: map[string]string{
				AnnProvisionedBy:                      config.ProvisionerName,
				v1.AlphaStorageNodeAffinityAnnotation: config.AffinityAnn,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
			Capacity: v1.ResourceList{
				// TODO: detect capacity
				v1.ResourceName(v1.ResourceStorage): resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				Local: &v1.LocalVolumeSource{
					Path: config.HostPath,
				},
			},
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			StorageClassName: config.StorageClass,
		},
	}
}
