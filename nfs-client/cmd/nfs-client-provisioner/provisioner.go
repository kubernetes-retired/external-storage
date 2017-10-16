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
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	provisionerNameKey = "PROVISIONER_NAME"

	annCreatedBy = "kubernetes.io/createdby"
	createdBy    = "nfs-client-dynamic-provisioner"
	mountOptionAnnotation = "volume.beta.kubernetes.io/mount-options"
	storageClassAnnotation = "volume.beta.kubernetes.io/storage-class"
)

type nfsProvisioner struct {
	client kubernetes.Interface
	server string
	path   string
	pvc_annot bool
}

const (
	mountPath = "/persistentvolumes"
)

var _ controller.Provisioner = &nfsProvisioner{}

// This function will gather the annotations we want from the PVC
// only volume.beta.kubernetes.io/mount-options is supported at the moment
func (p *nfsProvisioner) getAnnotationsFromVolumClaim(options controller.VolumeOptions) (map[string]string) {
	annotations := make(map[string]string)
	if p.pvc_annot {
		if val, ok := options.PVC.Annotations[mountOptionAnnotation]; ok {
			annotations[mountOptionAnnotation] = val
		}
	}
	return annotations
}

// This function will gather the annotations we want from the storage class
// only volume.beta.kubernetes.io/mount-options is supported at the moment
// It will only gathers them if PVC_ANNOT is set to 1
func (p *nfsProvisioner) getAnnotationsFromStorageClass(options controller.VolumeOptions) (map[string]string) {
	annotations := make(map[string]string)
	if val, ok := options.PVC.Annotations[storageClassAnnotation]; ok {
		glog.Infof("Handling storage class annotations")
		var soptions metav1.GetOptions
		result, err := p.client.StorageV1().StorageClasses().Get(val, soptions)
		if err != nil {
			panic(err.Error())
		}
		if val, ok := result.Annotations[mountOptionAnnotation]; ok {
			annotations[mountOptionAnnotation] = val
		}
	}
	return annotations
}

// This function will gather the annotations we want from the PVC and the storage class to merge it
// In the merge operation if an annotations is present in both of them the storage class one will prevail
func (p *nfsProvisioner) getAnnotations(options controller.VolumeOptions) (map[string]string) {
	glog.Infof("Handling annotations")
	annotations := make(map[string]string)
	annotations[annCreatedBy] = createdBy
	pvc_annotations := p.getAnnotationsFromVolumClaim(options)
	sc_annotations := p.getAnnotationsFromStorageClass(options)
	for k, v := range pvc_annotations {
		annotations[k] = v
	}
	for k, v := range sc_annotations {
		annotations[k] = v
	}
	return annotations
}

func (p *nfsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}
	glog.V(4).Infof("nfs provisioner: VolumeOptions %v", options)

	pvcNamespace := options.PVC.Namespace
	pvcName := options.PVC.Name

	pvName := strings.Join([]string{pvcNamespace, pvcName, options.PVName}, "-")

	fullPath := filepath.Join(mountPath, pvName)
	glog.V(4).Infof("creating path %s", fullPath)
	if err := os.MkdirAll(fullPath, 0777); err != nil {
		return nil, errors.New("unable to create directory to provision new pv: " + err.Error())
	}
	os.Chmod(fullPath, 0777)

	path := filepath.Join(p.path, pvName)


	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: p.getAnnotations(options),
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   p.server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}
	return pv, nil
}

func (p *nfsProvisioner) Delete(volume *v1.PersistentVolume) error {
	path := volume.Spec.PersistentVolumeSource.NFS.Path
	pvName := filepath.Base(path)
	oldPath := filepath.Join(mountPath, pvName)
	archivePath := filepath.Join(mountPath, "archived-"+pvName)
	glog.V(4).Infof("archiving path %s to %s", oldPath, archivePath)
	return os.Rename(oldPath, archivePath)
}

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	server := os.Getenv("NFS_SERVER")
	if server == "" {
		glog.Fatal("NFS_SERVER not set")
	}
	path := os.Getenv("NFS_PATH")
	if path == "" {
		glog.Fatal("NFS_PATH not set")
	}

	// By default gathering annotations from the PVC is disabled
	use_pvc_annot := false
	pvc_annot_env := os.Getenv("PVC_ANNOT")
	if pvc_annot_env == "1" {
		use_pvc_annot = true
	}
	provisionerName := os.Getenv(provisionerNameKey)
	if provisionerName == "" {
		glog.Fatalf("environment variable %s is not set! Please set it.", provisionerNameKey)
	}

	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	clientNFSProvisioner := &nfsProvisioner{
		server: server,
		path:   path,
		client: clientset,
		pvc_annot: use_pvc_annot,
	}
	// Start the provision controller which will dynamically provision efs NFS
	// PVs
	pc := controller.NewProvisionController(clientset, provisionerName, clientNFSProvisioner, serverVersion.GitVersion)
	pc.Run(wait.NeverStop)
}
