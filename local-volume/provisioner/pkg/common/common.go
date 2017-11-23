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
	"fmt"
	"io/ioutil"
	"path"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet/apis"
	"os"
	"path/filepath"
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
	// DefaultBlockCleanerCommand is the default block device cleaning command
	DefaultBlockCleanerCommand = "/scripts/quick_reset.sh"

	// EventVolumeFailedDelete copied from k8s.io/kubernetes/pkg/controller/volume/events
	EventVolumeFailedDelete = "VolumeFailedDelete"
	// ProvisionerConfigPath points to the path inside of the provisioner container where configMap volume is mounted
	ProvisionerConfigPath = "/etc/provisioner/config/"
	// ProvisonerStorageClassConfig defines file name of the file which stores storage class
	// configuration. The file name must match to the key name used in configuration map.
	ProvisonerStorageClassConfig = "storageClassMap"
	// ProvisionerNodeLabelsForPV contains a list of node labels to be copied to the PVs created by the provisioner
	ProvisionerNodeLabelsForPV = "nodeLabelsForPV"
	// VolumeDelete copied from k8s.io/kubernetes/pkg/controller/volume/events
	VolumeDelete = "VolumeDelete"

	// LocalPVEnv will contain the device path when script is invoked
	LocalPVEnv = "LOCAL_PV_BLKDEVICE"
	// KubeConfigEnv will (optionally) specify the location of kubeconfig file on the node.
	KubeConfigEnv = "KUBECONFIG"
)

// UserConfig stores all the user-defined parameters to the provisioner
type UserConfig struct {
	// Node object for this node
	Node *v1.Node
	// key = storageclass, value = mount configuration for the storageclass
	DiscoveryMap map[string]MountConfig
	// Labels and their values that are added to PVs created by the provisioner
	NodeLabelsForPV []string
}

// MountConfig stores a configuration for discoverying a specific storageclass
type MountConfig struct {
	// The hostpath directory
	HostDir string `json:"hostDir" yaml:"hostDir"`
	// The mount point of the hostpath volume
	MountDir string `json:"mountDir" yaml:"mountDir"`
	// The type of block cleaner to use
	BlockCleanerCommand []string `json:"blockCleanerCommand" yaml:"blockCleanerCommand"`
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
	// Disable block device discovery and management if true
	BlockDisabled bool
}

// LocalPVConfig defines the parameters for creating a local PV
type LocalPVConfig struct {
	Name            string
	HostPath        string
	Capacity        int64
	StorageClass    string
	ProvisionerName string
	AffinityAnn     string
	Labels          map[string]string
}

// BuildConfigFromFlags being defined to enable mocking during unit testing
var BuildConfigFromFlags = clientcmd.BuildConfigFromFlags

// InClusterConfig being defined to enable mocking during unit testing
var InClusterConfig = rest.InClusterConfig

// ProvisionerConfiguration defines Provisioner configuration objects
// Each configuration key of the struct e.g StorageClassConfig is individually
// marshaled in VolumeConfigToConfigMapData.
// TODO Need to find a way to marshal the struct  more efficiently.
type ProvisionerConfiguration struct {
	// StorageClassConfig defines configuration of Provisioner's storage classes
	StorageClassConfig map[string]MountConfig `json:"storageClassMap" yaml:"storageClassMap"`
	// NodeLabelsForPV contains a list of node labels to be copied to the PVs created by the provisioner
	// +optional
	NodeLabelsForPV []string `json:"nodeLabelsForPV" yaml:"nodeLabelsForPV"`
}

// CreateLocalPVSpec returns a PV spec that can be used for PV creation
func CreateLocalPVSpec(config *LocalPVConfig) *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:   config.Name,
			Labels: config.Labels,
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

// GetContainerPath gets the local path (within provisioner container) of the PV
func GetContainerPath(pv *v1.PersistentVolume, config MountConfig) (string, error) {
	relativePath, err := filepath.Rel(config.HostDir, pv.Spec.Local.Path)
	if err != nil {
		return "", fmt.Errorf("Could not get relative path for pv %q: %v", pv.Name, err)
	}

	return filepath.Join(config.MountDir, relativePath), nil
}

// GetVolumeConfigFromConfigMap gets volume configuration from given configmap,
func GetVolumeConfigFromConfigMap(client *kubernetes.Clientset, namespace, name string, provisionerConfig *ProvisionerConfiguration) error {
	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	err = ConfigMapDataToVolumeConfig(configMap.Data, provisionerConfig)
	return err
}

// GetDefaultVolumeConfig returns the default volume configuration.
func GetDefaultVolumeConfig(provisionerConfig *ProvisionerConfiguration) {
	provisionerConfig.StorageClassConfig = map[string]MountConfig{
		"local-storage": {
			HostDir:             DefaultHostDir,
			MountDir:            DefaultMountDir,
			BlockCleanerCommand: []string{DefaultBlockCleanerCommand},
		},
	}
}

// VolumeConfigToConfigMapData converts volume config to configmap data.
func VolumeConfigToConfigMapData(config *ProvisionerConfiguration) (map[string]string, error) {
	configMapData := make(map[string]string)
	val, err := yaml.Marshal(config.StorageClassConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to Marshal volume config: %v", err)
	}
	configMapData[ProvisonerStorageClassConfig] = string(val)
	if len(config.NodeLabelsForPV) > 0 {
		nodeLabels, err := yaml.Marshal(config.NodeLabelsForPV)
		if err != nil {
			return nil, fmt.Errorf("unable to Marshal node label: %v", err)
		}
		configMapData[ProvisionerNodeLabelsForPV] = string(nodeLabels)
	}

	return configMapData, nil
}

// ConfigMapDataToVolumeConfig converts configmap data to volume config
func ConfigMapDataToVolumeConfig(data map[string]string, provisionerConfig *ProvisionerConfiguration) error {
	rawYaml := ""
	for key, val := range data {
		rawYaml += key
		rawYaml += ": \n"
		rawYaml += insertSpaces(string(val))
	}

	if err := yaml.Unmarshal([]byte(rawYaml), provisionerConfig); err != nil {
		return fmt.Errorf("fail to Unmarshal yaml due to: %#v", err)
	}
	for class, config := range provisionerConfig.StorageClassConfig {
		if config.BlockCleanerCommand == nil {
			// supply a default block cleaner command.
			config.BlockCleanerCommand = []string{DefaultBlockCleanerCommand}
		} else {
			// Validate that array is non empty
			if len(config.BlockCleanerCommand) < 1 {
				return fmt.Errorf("Invalid empty block cleaner command for class %v", class)
			}
		}
		provisionerConfig.StorageClassConfig[class] = config
	}
	return nil
}

func insertSpaces(original string) string {
	spaced := ""
	for _, line := range strings.Split(original, "\n") {
		spaced += "   "
		spaced += line
		spaced += "\n"
	}
	return spaced
}

// LoadProvisionerConfigs loads all configuration into a string and unmarshal it into ProvisionerConfiguration struct.
// The configuration is stored in the configmap which is mounted as a volume.
func LoadProvisionerConfigs(provisionerConfig *ProvisionerConfiguration) error {
	files, err := ioutil.ReadDir(ProvisionerConfigPath)
	if err != nil {
		return err
	}
	data := make(map[string]string)
	for _, file := range files {
		if !file.IsDir() {
			fileContents, err := ioutil.ReadFile(path.Join(ProvisionerConfigPath, file.Name()))
			if err != nil {
				// TODO Currently errors in reading configuration keys/files are ignored. This is due to
				// the precense of "..data" file, it is a symbolic link which gets created when kubelet mounts a
				// configmap inside of a container. It needs to be revisited later for a safer solution, if ignoring read errors
				// will prove to be causing issues.
				glog.Infof("Could not read file: %s due to: %v", path.Join(ProvisionerConfigPath, file.Name()), err)
				continue
			}
			data[file.Name()] = string(fileContents)
		}
	}
	return ConfigMapDataToVolumeConfig(data, provisionerConfig)
}

// SetupClient created client using either in-cluster configuration or if KUBECONFIG environment variable is specified then using that config.
func SetupClient() *kubernetes.Clientset {
	var config *rest.Config
	var err error

	kubeconfigFile := os.Getenv(KubeConfigEnv)
	if kubeconfigFile != "" {
		config, err = BuildConfigFromFlags("", kubeconfigFile)
		if err != nil {
			glog.Fatalf("Error creating config from %s specified file: %s %v\n", KubeConfigEnv,
				kubeconfigFile, err)
		}
		glog.Infof("Creating client using kubeconfig file %s", kubeconfigFile)
	} else {
		config, err = InClusterConfig()
		if err != nil {
			glog.Fatalf("Error creating InCluster config: %v\n", err)
		}
		glog.Infof("Creating client using in-cluster config")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Error creating clientset: %v\n", err)
	}
	return clientset
}
