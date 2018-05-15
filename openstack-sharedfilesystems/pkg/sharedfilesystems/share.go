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

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/sharebackends"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/shareoptions"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/controller/volume/persistentvolume"
)

const (
	// ManilaAnnotationShareIDName identifies provisioned Share ID
	ManilaAnnotationShareIDName = "manila.external-storage.incubator.kubernetes.io/" + "ID"
	// shareAvailabilityTimeout is a timeout in secs for waiting until a newly created share becomes available.
	shareAvailabilityTimeout = 120 /* secs */
)

func createShare(
	volOptions *controller.VolumeOptions,
	shareOptions *shareoptions.ShareOptions,
	client *gophercloud.ServiceClient,
) (*shares.Share, error) {
	req, err := buildCreateRequest(volOptions, shareOptions)
	if err != nil {
		return nil, err
	}

	return shares.Create(client, *req).Extract()
}

func deleteShare(shareId string, client *gophercloud.ServiceClient, clientset *kubernetes.Clientset) error {
	r := shares.Delete(client, shareId)
	if r.Err != nil {
		return r.Err
	}

	if backendName, err := getBackendNameForShare(shareId); err == nil {
		shareBackend, err := getShareBackend(backendName)
		if err != nil {
			return err
		}

		if err = shareBackend.Release(&sharebackends.ReleaseArgs{ShareID: shareId, Clientset: clientset}); err != nil {
			return err
		}
	}

	return nil
}

func buildCreateRequest(volOptions *controller.VolumeOptions, shareOptions *shareoptions.ShareOptions) (*shares.CreateOpts, error) {
	storageSize, err := getStorageSizeInGiga(volOptions.PVC)
	if err != nil {
		return nil, fmt.Errorf("couldn't retrieve PVC storage size: %v", err)
	}

	return &shares.CreateOpts{
		ShareProto: shareOptions.Protocol,
		Size:       storageSize,
		Name:       shareOptions.ShareName,
		ShareType:  shareOptions.Type,
		Metadata: map[string]string{
			persistentvolume.CloudVolumeCreatedForClaimNamespaceTag: volOptions.PVC.Namespace,
			persistentvolume.CloudVolumeCreatedForClaimNameTag:      volOptions.PVC.Name,
			persistentvolume.CloudVolumeCreatedForVolumeNameTag:     shareOptions.ShareName,
		},
	}, nil
}

func buildPersistentVolume(share *shares.Share, volOptions *controller.VolumeOptions, volSource *v1.PersistentVolumeSource) *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        volOptions.PVName,
			Annotations: map[string]string{ManilaAnnotationShareIDName: share.ID},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: volOptions.PersistentVolumeReclaimPolicy,
			AccessModes:                   getPVAccessMode(volOptions.PVC.Spec.AccessModes),
			Capacity:                      v1.ResourceList{v1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dG", share.Size))},
			PersistentVolumeSource:        *volSource,
		},
	}
}
