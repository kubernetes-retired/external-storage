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

package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	"k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultImageName          = "quay.io/external_storage/local-volume-provisioner:latest"
	defaultVolumeConfigName   = "local-volume-default-config"
	defaultServiceAccountName = "local-storage-admin"
	defaultMountRoot          = "/mnt/local-storage"

	daemonSetName = "local-volume-provisioner"
	containerName = "provisioner"

	pvBindingName           = "local-storage:provisioner-pv-binding"
	nodeBindingName         = "local-storage:provisioner-node-binding"
	systemRoleNode          = "system:node"
	systemRolePVProvisioner = "system:persistent-volume-provisioner"
)

var (
	imageName          = flag.String("image", defaultImageName, "Name of local volume provisioner image")
	mountRoot          = flag.String("mount-root", defaultMountRoot, "Container root directory of volume mount path for discoverying local volumes. This is used only when mountDir is omitted in volume configmap, in which case hostDir will be normalized then concatenates with mountRoot")
	volumeConfigName   = flag.String("volume-config", defaultVolumeConfigName, "Name of the local volume configuration configmap. The configmap must reside in the same namespace with bootstrapper.")
	serviceAccountName = flag.String("serviceaccount", defaultServiceAccountName, "Name of the service accout for local volume provisioner")
)

var provisionerConfig common.ProvisionerConfiguration

// setupClient creates an in cluster kubernetes client.
func setupClient() *kubernetes.Clientset {
	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("Error creating InCluster config: %v\n", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Error creating clientset: %v\n", err)
	}
	return clientset
}

// generateMountName generates a volumeMount.name for pod spec, based on volume configuration.
func generateMountName(mount *common.MountConfig) string {
	h := fnv.New32a()
	h.Write([]byte(mount.HostDir))
	h.Write([]byte(mount.MountDir))
	return fmt.Sprintf("mount-%x", h.Sum32())
}

// generateMountDir generates mount directory path in container. The rule is to trim '/' prefix
// and change "/" to "~" (based on kubernetes convention), then concatenate with root path, e.g,
// if mountRoot == /mnt/local-storage, then:
//   "/mnt/ssds" -> "/mnt/local-storage/mnt~ssds"
func generateMountDir(mount *common.MountConfig) string {
	return path.Join(*mountRoot, strings.Replace(strings.TrimPrefix(mount.HostDir, "/"), "/", "~", -1))
}

// ensureVolumeConfig reads volume configurations from given configmap, and create a default one
// if not already exist.
func ensureVolumeConfig(client *kubernetes.Clientset, namespace string, config *common.ProvisionerConfiguration) error {
	// Get config map from user or from a default configmap (if created before).
	err := common.GetVolumeConfigFromConfigMap(client, namespace, *volumeConfigName, config)
	if err != nil && *volumeConfigName != defaultVolumeConfigName {
		// If configmap is provided by user but we have problem getting it, fail fast.
		return fmt.Errorf("could not get config map: %v", err)
	} else if err != nil && errors.IsNotFound(err) {
		// configmap is not provided by user and default configmap doesn't exist, create one.
		glog.Infof("No config is given, creating default configmap %v", *volumeConfigName)
		common.GetDefaultVolumeConfig(config)
		if err = createConfigMap(client, namespace, config); err != nil {
			return fmt.Errorf("unable to create configmap: %v", err)
		}
	} else if err != nil {
		// error exists, it might be that default configmap is damanged, fail fast.
		return fmt.Errorf("could not get default config map: %v", err)
	}
	return nil
}

// ensureMountDir checks if each storageclass's mount config has 'MountDir' set; if not, it will
// automatically generate one and informs an update on configmap is required.
func ensureMountDir(config *common.ProvisionerConfiguration) bool {
	needsUpdate := false
	for class, mount := range config.StorageClassConfig {
		if mount.MountDir == "" {
			newMoutConfig := mount
			newMoutConfig.MountDir = generateMountDir(&mount)
			config.StorageClassConfig[class] = newMoutConfig
			needsUpdate = true
		}
	}
	return needsUpdate
}

// updateConfigMap ...
func updateConfigMap(client *kubernetes.Clientset, namespace string, config *common.ProvisionerConfiguration) error {
	var err error
	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(*volumeConfigName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	configMap.Data, err = common.VolumeConfigToConfigMapData(config)
	if err != nil {
		return err
	}
	_, err = client.CoreV1().ConfigMaps(namespace).Update(configMap)
	return err
}

// createConfigMap ...
func createConfigMap(client *kubernetes.Clientset, namespace string, config *common.ProvisionerConfiguration) error {
	data, err := common.VolumeConfigToConfigMapData(config)
	if err != nil {
		glog.Fatalf("Unable to convert volume config to configmap %v\n", err)
	}
	configMap := v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      *volumeConfigName,
			Namespace: namespace,
		},
		Data: data,
	}
	_, err = client.CoreV1().ConfigMaps(namespace).Create(&configMap)
	return err
}

// createServiceAccount ...
func createServiceAccount(client *kubernetes.Clientset, namespace string) error {
	serviceAccount := v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      *serviceAccountName,
			Namespace: namespace,
		},
	}
	_, err := client.CoreV1().ServiceAccounts(namespace).Create(&serviceAccount)
	return err
}

// createClusterRoleBinding creates two cluster role bindings for local volume provisioner's
// service account: systemRoleNode and systemRolePVProvisioner. These are required for
// provisioner to get node information and create persistent volumes.
func createClusterRoleBinding(client *kubernetes.Clientset, namespace string) error {
	subjects := []rbacv1beta1.Subject{
		{
			Kind:      rbacv1beta1.ServiceAccountKind,
			Name:      *serviceAccountName,
			Namespace: namespace,
		},
	}

	pvBinding := rbacv1beta1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1beta1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: pvBindingName,
		},
		RoleRef: rbacv1beta1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     systemRolePVProvisioner,
		},
		Subjects: subjects,
	}
	nodeBinding := rbacv1beta1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1beta1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeBindingName,
		},
		RoleRef: rbacv1beta1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     systemRoleNode,
		},
		Subjects: subjects,
	}

	_, err := client.RbacV1beta1().ClusterRoleBindings().Create(&pvBinding)
	if err != nil {
		return err
	}
	_, err = client.RbacV1beta1().ClusterRoleBindings().Create(&nodeBinding)
	if err != nil {
		return err
	}
	return nil
}

// createDaemonSet ...
func createDaemonSet(client *kubernetes.Clientset, namespace string, config *common.ProvisionerConfiguration) error {
	volumes := []v1.Volume{}
	volumeMounts := []v1.VolumeMount{}
	for _, mount := range config.StorageClassConfig {
		name := generateMountName(&mount)
		volumes = append(volumes, v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: mount.HostDir,
				},
			},
		})
		volumeMounts = append(volumeMounts, v1.VolumeMount{
			Name:      name,
			MountPath: mount.MountDir,
		})
	}
	// Appending configMap as a volume
	volumes = append(volumes, v1.Volume{
		Name: *volumeConfigName,
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: *volumeConfigName,
				},
			},
		},
	})
	// Appending a mount point for ConfigMap Volume
	volumeMounts = append(volumeMounts, v1.VolumeMount{
		Name:      *volumeConfigName,
		MountPath: common.ProvisionerConfigPath,
	})

	envVars := []v1.EnvVar{
		{
			Name: "MY_NODE_NAME",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					FieldPath: "spec.nodeName",
				},
			},
		},
		{
			Name: "MY_NAMESPACE",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
	}

	var priv = true
	securityContext := &v1.SecurityContext{
		Privileged: &priv,
	}

	containers := []v1.Container{
		{
			Name:            containerName,
			Image:           *imageName,
			VolumeMounts:    volumeMounts,
			Env:             envVars,
			SecurityContext: securityContext,
		},
	}

	daemonSet := extv1beta1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions/v1beta1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      daemonSetName,
			Namespace: namespace,
		},
		Spec: extv1beta1.DaemonSetSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": daemonSetName},
				},
				Spec: v1.PodSpec{
					Volumes:            volumes,
					Containers:         containers,
					ServiceAccountName: *serviceAccountName,
				},
			},
		},
	}
	_, err := client.Extensions().DaemonSets(namespace).Create(&daemonSet)
	return err
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	namespace := os.Getenv("MY_NAMESPACE")
	if namespace == "" {
		glog.Fatalf("MY_NAMESPACE environment variable not set\n")
	}

	client := setupClient()

	var err error

	provisionerConfig = common.ProvisionerConfiguration{
		StorageClassConfig: make(map[string]common.MountConfig),
	}
	if err = ensureVolumeConfig(client, namespace, &provisionerConfig); err != nil {
		glog.Fatalf("Unable to ensure volume config: %v", err)
	}
	if ensureMountDir(&provisionerConfig) {
		if err = updateConfigMap(client, namespace, &provisionerConfig); err != nil {
			glog.Fatalf("Could not update config map to use generated mountdir: %v", err)
		}
	}
	glog.Infof("Running bootstrap pod with config %v: %+v\n", *volumeConfigName, provisionerConfig)

	if err = createServiceAccount(client, namespace); err != nil && !errors.IsAlreadyExists(err) {
		glog.Fatalf("Unable to create service account: %v\n", err)
	}
	if err = createClusterRoleBinding(client, namespace); err != nil && !errors.IsAlreadyExists(err) {
		glog.Fatalf("Unable to create cluster role bindings: %v\n", err)
	}
	if err = createDaemonSet(client, namespace, &provisionerConfig); err != nil {
		glog.Fatalf("Unable to create daemonset: %v\n", err)
	}

	glog.Infof("Successfully created local volume provisioner")
}
