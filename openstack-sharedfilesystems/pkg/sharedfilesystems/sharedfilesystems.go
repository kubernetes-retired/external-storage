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
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/shareoptions"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/controller/volume/persistentvolume"
)

const (
	// ManilaAnnotationShareIDName identifies provisioned Share ID
	ManilaAnnotationShareIDName = "manila.external-storage.incubator.kubernetes.io/" + "ID"
	// shareAvailabilityTimeout is a timeout in secs for waiting until a newly created share becomes available.
	shareAvailabilityTimeout = 120 /* secs */
)

// PrepareCreateRequest return:
// - success: ready to send shared filesystem create request data structure constructed from Persistent Volume Claim and corresponding Storage Class
// - failure: an error
func PrepareCreateRequest(volOptions *controller.VolumeOptions, shareOptions *shareoptions.ShareOptions) (*shares.CreateOpts, error) {
	var (
		storageSize int
		err         error
	)

	if storageSize, err = getPVCStorageSize(volOptions.PVC); err != nil {
		return nil, err
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

// WaitTillAvailable keeps querying Manila API for a share status until it is available. The waiting can:
// - succeed: in this case the is/becomes available
// - timeout: error is returned.
// - another error occurs: error is returned.
func WaitTillAvailable(client *gophercloud.ServiceClient, shareID string) error {
	desiredStatus := "available"
	return gophercloud.WaitFor(shareAvailabilityTimeout, func() (bool, error) {
		current, err := shares.Get(client, shareID).Extract()
		if err != nil {
			return false, err
		}

		if current.Status == desiredStatus {
			return true, nil
		}
		return false, nil
	})
}

// ChooseExportLocation chooses one ExportLocation according to the below rules:
// 1. Path is not empty, i.e. is not an empty string or does not contain spaces and tabs only
// 2. IsAdminOnly == false
// 3. Preferred == true are preferred over Preferred == false
// 4. Locations with lower slice index are preferred over locations with higher slice index
// In case no location complies with the above rules an error is returned.
func ChooseExportLocation(locs []shares.ExportLocation) (shares.ExportLocation, error) {
	if len(locs) == 0 {
		return shares.ExportLocation{}, fmt.Errorf("Error: received an empty list of export locations")
	}
	foundMatchingNotPreferred := false
	var matchingNotPreferred shares.ExportLocation
	for _, loc := range locs {
		if loc.IsAdminOnly || strings.TrimSpace(loc.Path) == "" {
			continue
		}
		if loc.Preferred {
			return loc, nil
		}
		if !foundMatchingNotPreferred {
			matchingNotPreferred = loc
			foundMatchingNotPreferred = true
		}
	}
	if foundMatchingNotPreferred {
		return matchingNotPreferred, nil
	}
	return shares.ExportLocation{}, fmt.Errorf("cannot find any non-admin export location")
}

func CreatePersistentVolumeRequest(share *shares.Share, volOptions *controller.VolumeOptions, volSource *v1.PersistentVolumeSource) *v1.PersistentVolume {
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

// GetShareIDfromPV returns:
// - an error in case there is no shareID stored in volume.ObjectMeta.Annotations[ManilaAnnotationShareIDName]
func GetShareIDfromPV(volume *v1.PersistentVolume) (string, error) {
	if shareID, exists := volume.ObjectMeta.Annotations[ManilaAnnotationShareIDName]; exists {
		return shareID, nil
	}
	return "", fmt.Errorf("did not find share ID in annotatins in PV (%v)", volume)
}
