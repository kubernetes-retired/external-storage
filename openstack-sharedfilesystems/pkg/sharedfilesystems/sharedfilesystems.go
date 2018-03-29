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
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/controller/volume/persistentvolume"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"
)

const (
	// ZonesSCParamName is the name of the Storage Class parameter in which a set of zones is specified.
	// The persistent volume will be dynamically provisioned in one of these zones.
	ZonesSCParamName = "zones"
	// TypeSCParamName is the name of a share type configured by administrator of Manila service.
	TypeSCParamName = "type"
	// ProtocolNFS is the NFS shared filesystems protocol
	ProtocolNFS = "NFS"
	// ManilaAnnotationShareIDName identifies provisioned Share ID
	ManilaAnnotationShareIDName = "manila.external-storage.incubator.kubernetes.io/" + "ID"
	// shareAvailabilityTimeout is a timeout in secs for waiting until a newly created share becomes available.
	shareAvailabilityTimeout = 120 /* secs */
)

func getPVCStorageSize(pvc *v1.PersistentVolumeClaim) (int, error) {
	var storageSize resource.Quantity
	var ok bool
	errStorageSizeNotConfigured := fmt.Errorf("requested storage capacity must be set")
	if pvc.Spec.Resources.Requests == nil {
		return 0, errStorageSizeNotConfigured
	}
	if storageSize, ok = pvc.Spec.Resources.Requests[v1.ResourceStorage]; !ok {
		return 0, errStorageSizeNotConfigured
	}
	if storageSize.IsZero() {
		return 0, fmt.Errorf("requested storage size must not have zero value")
	}
	if storageSize.Sign() == -1 {
		return 0, fmt.Errorf("requested storage size must be greater than zero")
	}
	canonicalValue, _ := storageSize.AsScale(resource.Giga)
	var buf []byte
	storageSizeAsByte, _ := canonicalValue.AsCanonicalBytes(buf)
	var i int
	var err error
	if i, err = strconv.Atoi(string(storageSizeAsByte)); err != nil {
		return 0, fmt.Errorf("requested storage size is not a number")
	}
	return i, nil
}

// PrepareCreateRequest return:
// - success: ready to send shared filesystem create request data structure constructed from Persistent Volume Claim and corresponding Storage Class
// - failure: an error
func PrepareCreateRequest(options controller.VolumeOptions) (shares.CreateOpts, error) {
	var storageSize int
	var err error
	// mandatory parameter
	if storageSize, err = getPVCStorageSize(options.PVC); err != nil {
		return shares.CreateOpts{}, err
	}

	shareType := ""
	zone := "nova"
	for index, value := range options.Parameters {
		switch strings.ToLower(index) {
		case ZonesSCParamName:
			setOfZones, err := util.ZonesToSet(value)
			if err != nil {
				return shares.CreateOpts{}, err
			}
			zone = volume.ChooseZoneForVolume(setOfZones, options.PVC.Name)
		case TypeSCParamName:
			shareType = value
		default:
			return shares.CreateOpts{}, fmt.Errorf("invalid parameter %q", index)
		}
	}

	shareName := "pvc-" + string(options.PVC.UID)
	// Currently only the NFS shares are supported, that's why the NFS is hardcoded.
	return shares.CreateOpts{
		ShareProto: ProtocolNFS,
		Size:       storageSize,
		Name:       shareName,
		ShareType:  shareType,
		Metadata: map[string]string{
			persistentvolume.CloudVolumeCreatedForClaimNamespaceTag: options.PVC.Namespace,
			persistentvolume.CloudVolumeCreatedForClaimNameTag:      options.PVC.Name,
			persistentvolume.CloudVolumeCreatedForVolumeNameTag:     shareName,
		},
		AvailabilityZone: zone,
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

// FillInPV creates the PV data structure from original PVC, provisioned share and the share export location
func FillInPV(options controller.VolumeOptions, share shares.Share, exportLocation shares.ExportLocation) (*v1.PersistentVolume, error) {

	storageSize := resource.MustParse(fmt.Sprintf("%dG", share.Size))
	PVAccessMode := getPVAccessMode(options.PVC.Spec.AccessModes)
	server, path, err := getServerAndPath(exportLocation.Path)
	if err != nil {
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				ManilaAnnotationShareIDName: share.ID,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   PVAccessMode,
			Capacity: v1.ResourceList{
				v1.ResourceStorage: storageSize,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

// GetShareIDfromPV returns:
// - an error in case there is no shareID stored in volume.ObjectMeta.Annotations[ManilaAnnotationShareIDName]
func GetShareIDfromPV(volume *v1.PersistentVolume) (string, error) {
	if shareID, exists := volume.ObjectMeta.Annotations[ManilaAnnotationShareIDName]; exists {
		return shareID, nil
	}
	return "", fmt.Errorf("did not find share ID in annotatins in PV (%v)", volume)
}

// FIXME: for IPv6
func getServerAndPath(exportLocationPath string) (string, string, error) {
	split := strings.SplitN(exportLocationPath, ":", 2)
	if len(split) == 2 {
		return split[0], split[1], nil
	}
	return "", "", fmt.Errorf("failed to split export location %q into server and path parts", exportLocationPath)
}

func getPVAccessMode(PVCAccessMode []v1.PersistentVolumeAccessMode) []v1.PersistentVolumeAccessMode {
	if len(PVCAccessMode) > 0 {
		return PVCAccessMode
	}
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
		v1.ReadOnlyMany,
		v1.ReadWriteMany,
	}
}
