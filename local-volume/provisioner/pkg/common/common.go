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
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"hash/fnv"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet/apis"
	"k8s.io/kubernetes/pkg/util/mount"
)

const (
	// AnnProvisionedBy is the external provisioner annotation in PV object
	AnnProvisionedBy = "pv.kubernetes.io/provisioned-by"
	// NodeLabelKey is the label key that this provisioner uses for PV node affinity
	// hostname is not the best choice, but it's what pod and node affinity also use
	NodeLabelKey = apis.LabelHostname

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
	// ProvisionerUseAlphaAPI shows if we need to use alpha API, default to false
	ProvisionerUseAlphaAPI = "useAlphaAPI"
	// AlphaStorageNodeAffinityAnnotation defines node affinity policies for a PersistentVolume.
	// Value is a string of the json representation of type NodeAffinity
	AlphaStorageNodeAffinityAnnotation = "volume.alpha.kubernetes.io/node-affinity"
	// VolumeDelete copied from k8s.io/kubernetes/pkg/controller/volume/events
	VolumeDelete = "VolumeDelete"

	// LocalPVEnv will contain the device path when script is invoked
	LocalPVEnv = "LOCAL_PV_BLKDEVICE"
	// LocalFilesystemEnv will contain the filesystm path when script is invoked
	LocalFilesystemEnv = "LOCAL_PV_FILESYSTEM"
	// KubeConfigEnv will (optionally) specify the location of kubeconfig file on the node.
	KubeConfigEnv = "KUBECONFIG"

	// NodeNameLabel is the name of the label that holds the nodename
	NodeNameLabel = "kubernetes.io/hostname"
)

// UserConfig stores all the user-defined parameters to the provisioner
type UserConfig struct {
	// Node object for this node
	Node *v1.Node
	// key = storageclass, value = mount configuration for the storageclass
	DiscoveryMap map[string]MountConfig
	// Labels and their values that are added to PVs created by the provisioner
	NodeLabelsForPV []string
	// UseAlphaAPI shows if we need to use alpha API
	UseAlphaAPI bool
	// UseJobForCleaning indicates if Jobs should be spawned for cleaning block devices (as opposed to process),.
	UseJobForCleaning bool
	// Namespace of this Pod (optional)
	Namespace string
	// Image of container to use for jobs (optional)
	JobContainerImage string
	// MinResyncPeriod is minimum resync period. Resync period in reflectors
	// will be random between MinResyncPeriod and 2*MinResyncPeriod.
	MinResyncPeriod metav1.Duration
	// UseNodeNameOnly indicates if Node.Name should be used in the provisioner name
	// instead of Node.UID.
	UseNodeNameOnly bool
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
	Client kubernetes.Interface
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
	// Mounter used to verify mountpoints
	Mounter mount.Interface
	// InformerFactory gives access to informers for the controller.
	InformerFactory informers.SharedInformerFactory
}

// LocalPVConfig defines the parameters for creating a local PV
type LocalPVConfig struct {
	Name            string
	HostPath        string
	Capacity        int64
	StorageClass    string
	ReclaimPolicy   v1.PersistentVolumeReclaimPolicy
	ProvisionerName string
	UseAlphaAPI     bool
	AffinityAnn     string
	NodeAffinity    *v1.VolumeNodeAffinity
	VolumeMode      v1.PersistentVolumeMode
	Labels          map[string]string
}

// BuildConfigFromFlags being defined to enable mocking during unit testing
var BuildConfigFromFlags = clientcmd.BuildConfigFromFlags

// InClusterConfig being defined to enable mocking during unit testing
var InClusterConfig = rest.InClusterConfig

// ProvisionerConfiguration defines Provisioner configuration objects
// Each configuration key of the struct e.g StorageClassConfig is individually
// marshaled in VolumeConfigToConfigMapData.
// TODO Need to find a way to marshal the struct more efficiently.
type ProvisionerConfiguration struct {
	// StorageClassConfig defines configuration of Provisioner's storage classes
	StorageClassConfig map[string]MountConfig `json:"storageClassMap" yaml:"storageClassMap"`
	// NodeLabelsForPV contains a list of node labels to be copied to the PVs created by the provisioner
	// +optional
	NodeLabelsForPV []string `json:"nodeLabelsForPV" yaml:"nodeLabelsForPV"`
	// UseAlphaAPI shows if we need to use alpha API, default to false
	UseAlphaAPI bool `json:"useAlphaAPI" yaml:"useAlphaAPI"`
	// UseJobForCleaning indicates if Jobs should be spawned for cleaning block devices (as opposed to process),
	// default is false.
	// +optional
	UseJobForCleaning bool `json:"useJobForCleaning" yaml:"useJobForCleaning"`
	// MinResyncPeriod is minimum resync period. Resync period in reflectors
	// will be random between MinResyncPeriod and 2*MinResyncPeriod.
	MinResyncPeriod metav1.Duration `json:"minResyncPeriod" yaml:"minResyncPeriod"`
	// UseNodeNameOnly indicates if Node.Name should be used in the provisioner name
	// instead of Node.UID. Default is false.
	// +optional
	UseNodeNameOnly bool `json:"useNodeNameOnly" yaml:"useNodeNameOnly"`
}

// CreateLocalPVSpec returns a PV spec that can be used for PV creation
func CreateLocalPVSpec(config *LocalPVConfig) *v1.PersistentVolume {
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:   config.Name,
			Labels: config.Labels,
			Annotations: map[string]string{
				AnnProvisionedBy: config.ProvisionerName,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: config.ReclaimPolicy,
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
			VolumeMode:       &config.VolumeMode,
		},
	}
	if config.UseAlphaAPI {
		pv.ObjectMeta.Annotations[AlphaStorageNodeAffinityAnnotation] = config.AffinityAnn
	} else {
		pv.Spec.NodeAffinity = config.NodeAffinity
	}
	return pv
}

// GetContainerPath gets the local path (within provisioner container) of the PV
func GetContainerPath(pv *v1.PersistentVolume, config MountConfig) (string, error) {
	relativePath, err := filepath.Rel(config.HostDir, pv.Spec.Local.Path)
	if err != nil {
		return "", fmt.Errorf("Could not get relative path for pv %q: %v", pv.Name, err)
	}

	return filepath.Join(config.MountDir, relativePath), nil
}

// GetVolumeConfigFromConfigMap gets volume configuration from given configmap.
func GetVolumeConfigFromConfigMap(client *kubernetes.Clientset, namespace, name string, provisionerConfig *ProvisionerConfiguration) error {
	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	err = ConfigMapDataToVolumeConfig(configMap.Data, provisionerConfig)
	return err
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
		nodeLabels, nlErr := yaml.Marshal(config.NodeLabelsForPV)
		if nlErr != nil {
			return nil, fmt.Errorf("unable to Marshal node label: %v", nlErr)
		}
		configMapData[ProvisionerNodeLabelsForPV] = string(nodeLabels)
	}
	ver, err := yaml.Marshal(config.UseAlphaAPI)
	if err != nil {
		return nil, fmt.Errorf("unable to Marshal API version config: %v", err)
	}
	configMapData[ProvisionerUseAlphaAPI] = string(ver)

	return configMapData, nil
}

// ConfigMapDataToVolumeConfig converts configmap data to volume config.
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
			// Supply a default block cleaner command.
			config.BlockCleanerCommand = []string{DefaultBlockCleanerCommand}
		} else {
			// Validate that array is non empty.
			if len(config.BlockCleanerCommand) < 1 {
				return fmt.Errorf("Invalid empty block cleaner command for class %v", class)
			}
		}
		if config.MountDir == "" || config.HostDir == "" {
			return fmt.Errorf("Storage Class %v is misconfigured, missing HostDir or MountDir parameter", class)
		}
		provisionerConfig.StorageClassConfig[class] = config
		glog.Infof("StorageClass %q configured with MountDir %q, HostDir %q, BlockCleanerCommand %q",
			class,
			config.MountDir,
			config.HostDir,
			config.BlockCleanerCommand)
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
func LoadProvisionerConfigs(configPath string, provisionerConfig *ProvisionerConfiguration) error {
	files, err := ioutil.ReadDir(configPath)
	if err != nil {
		return err
	}
	data := make(map[string]string)
	for _, file := range files {
		if !file.IsDir() {
			if strings.Compare(file.Name(), "..data") != 0 {
				fileContents, err := ioutil.ReadFile(path.Join(configPath, file.Name()))
				if err != nil {
					glog.Infof("Could not read file: %s due to: %v", path.Join(configPath, file.Name()), err)
					return err
				}
				data[file.Name()] = string(fileContents)
			}
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

// GenerateMountName generates a volumeMount.name for pod spec, based on volume configuration.
func GenerateMountName(mount *MountConfig) string {
	h := fnv.New32a()
	h.Write([]byte(mount.HostDir))
	h.Write([]byte(mount.MountDir))
	return fmt.Sprintf("mount-%x", h.Sum32())
}
