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
	"encoding/json"
	"fmt"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet/apis"
)

const (
	// AnnProvisionedBy is the external provisioner annotation in PV object
	AnnProvisionedBy = "pv.kubernetes.io/provisioned-by"
	// NodeLabelKey is the label key that this provisioner uses for PV node affinity
	// hostname is not the best choice, but it's what pod and node affinity also use
	NodeLabelKey = apis.LabelHostname
	// VolumeTypeFile represents file type volumes
	VolumeTypeFile = "file"
	// VolumeTypeBlock represents block type volumes
	VolumeTypeBlock = "block"

	// DefaultHostDir is the default host dir to discover local volumes.
	DefaultHostDir = "/mnt/disks"
	// DefaultMountDir is the container mount point for the default host dir.
	DefaultMountDir = "/local-disks"

	// EventVolumeFailedDelete copied from k8s.io/kubernetes/pkg/controller/volume/events
	EventVolumeFailedDelete = "VolumeFailedDelete"
)

// UserConfig stores all the user-defined parameters to the provisioner
type UserConfig struct {
	// Node object for this node
	Node *v1.Node
	// key = storageclass, value = mount configuration for the storageclass
	DiscoveryMap map[string]MountConfig
}

// MountConfig stores a configuration for discoverying a specific storageclass
type MountConfig struct {
	// The hostpath directory
	HostDir string `json:"hostDir"`
	// The mount point of the hostpath volume
	MountDir string `json:"mountDir"`
}

// RuntimeConfig stores all the objects that the provisioner needs to run
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
	// Recorder is used to record events in the API server
	Recorder record.EventRecorder
}

// LocalPVConfig defines the parameters for creating a local PV
type LocalPVConfig struct {
	Name            string
	HostPath        string
	Capacity        int64
	StorageClass    string
	ProvisionerName string
	AffinityAnn     string
}

// CreateLocalPVSpec returns a PV spec that can be used for PV creation
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
				v1.ResourceName(v1.ResourceStorage): *resource.NewQuantity(int64(config.Capacity), resource.BinarySI),
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

// GetVolumeConfigFromConfigMap gets volume configuration from given configmap,
func GetVolumeConfigFromConfigMap(client *kubernetes.Clientset, namespace, name string) (map[string]MountConfig, error) {
	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return ConfigMapDataToVolumeConfig(configMap.Data)
}

// GetDefaultVolumeConfig returns the default volume configuration.
func GetDefaultVolumeConfig() map[string]MountConfig {
	return map[string]MountConfig{
		"local-storage": {
			HostDir:  DefaultHostDir,
			MountDir: DefaultMountDir,
		},
	}
}

// VolumeConfigToConfigMapData converts volume config to configmap data.
func VolumeConfigToConfigMapData(config map[string]MountConfig) (map[string]string, error) {
	configMapData := make(map[string]string)
	for class, data := range config {
		var val []byte
		var err error
		if val, err = json.Marshal(data); err != nil {
			return nil, fmt.Errorf("unable to unmarshal config for class %v: %v", class, err)
		}
		configMapData[class] = string(val)
	}
	return configMapData, nil
}

// ConfigMapDataToVolumeConfig converts configmap data to volume config
func ConfigMapDataToVolumeConfig(data map[string]string) (map[string]MountConfig, error) {
	mountConfig := make(map[string]MountConfig)
	for class, val := range data {
		config := MountConfig{}
		if err := json.Unmarshal([]byte(val), &config); err != nil {
			return nil, fmt.Errorf("unable to unmarshal config for class %v: %v", class, err)
		}
		mountConfig[class] = config
	}
	return mountConfig, nil
}
