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

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	extv1beta1 "k8s.io/client-go/pkg/apis/extensions/v1beta1"
	rbacv1beta1 "k8s.io/client-go/pkg/apis/rbac/v1beta1"
	"k8s.io/client-go/rest"
)

const (
	defaultImage                  = "local-volume-provisioner:dev"
	provisionerDaemonSetName      = "local-volume-provisioner"
	provisionerContainerName      = "provisioner"
	provisionerServiceAccountName = "local-storage-admin"

	provisionerPVBindingName   = "local-storage:provisioner-pv-binding"
	provisionerNodeBindingName = "local-storage:provisioner-node-binding"
	systemRoleNode             = "system:node"
	systemRolePVProvisioner    = "system:persistent-volume-provisioner"
)

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

func generateMountName(hostDir, mountDir string) string {
	h := fnv.New32a()
	h.Write([]byte(hostDir))
	h.Write([]byte(mountDir))
	return fmt.Sprintf("mount-%x", h.Sum32())
}

func createServiceAccount(client *kubernetes.Clientset, namespace string) error {
	serviceAccount := v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerServiceAccountName,
			Namespace: namespace,
		},
	}
	_, err := client.CoreV1().ServiceAccounts(namespace).Create(&serviceAccount)
	return err
}

func createClusterRoleBinding(client *kubernetes.Clientset, namespace string) error {
	subjects := []rbacv1beta1.Subject{
		{
			Kind:      rbacv1beta1.ServiceAccountKind,
			Name:      provisionerServiceAccountName,
			Namespace: namespace,
		},
	}

	pvBinding := rbacv1beta1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1beta1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: provisionerPVBindingName,
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
			Name: provisionerNodeBindingName,
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

func createDaemonSet(client *kubernetes.Clientset, namespace string, config map[string]common.MountConfig) error {
	volumes := []v1.Volume{}
	volumeMounts := []v1.VolumeMount{}
	for _, mount := range config {
		name := generateMountName(mount.HostDir, mount.MountDir)
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
		{
			Name:  "VOLUME_CONFIG_NAME",
			Value: os.Getenv("VOLUME_CONFIG_NAME"),
		},
	}

	containers := []v1.Container{
		{
			Name:         provisionerContainerName,
			Image:        defaultImage,
			VolumeMounts: volumeMounts,
			Env:          envVars,
		},
	}

	// TODO: make daemonset configurable as well, using another configmap.
	daemonSet := extv1beta1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions/v1beta1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      provisionerDaemonSetName,
			Namespace: namespace,
		},
		Spec: extv1beta1.DaemonSetSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": provisionerDaemonSetName},
				},
				Spec: v1.PodSpec{
					Volumes:            volumes,
					Containers:         containers,
					ServiceAccountName: provisionerServiceAccountName,
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
	volumeConfigName := os.Getenv("VOLUME_CONFIG_NAME")
	if volumeConfigName == "" {
		glog.Fatalf("VOLUME_CONFIG_NAME environment variable not set\n")
	}

	client := setupClient()
	config, err := common.GetVolumeConfig(client, namespace, volumeConfigName)
	if err != nil {
		glog.Fatalf("Could not get config map information: %v", err)
	}

	glog.Infof("Running bootstrap pod with config %+v\n", config)

	// TODO: check error and clean up resources.
	if err := createServiceAccount(client, namespace); err != nil && !errors.IsAlreadyExists(err) {
		glog.Fatalf("Unable to create service account: %v\n", err)
	}
	if err := createClusterRoleBinding(client, namespace); err != nil && !errors.IsAlreadyExists(err) {
		glog.Fatalf("Unable to create cluster role bindings: %v\n", err)
	}
	if err := createDaemonSet(client, namespace, config); err != nil {
		glog.Fatalf("Unable to create daemonset: %v\n", err)
	}

	glog.Infof("Successfully created local volume provisioner")
}
