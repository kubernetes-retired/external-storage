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
	"reflect"
	"testing"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/controller/volume/persistentvolume"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"

	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
)

const (
	fakeUID           = types.UID("unique-uid")
	fakeShareName     = "pvc-" + string(fakeUID)
	fakePVCName       = "pvc"
	fakeNamespace     = "foo"
	fakeShareID       = "de64eb77-05cb-4502-a6e5-7e8552c352f3"
	fakeReclaimPolicy = "Delete"
	fakeZoneName      = "nova"
	fakePVName        = "pv"
	fakeShareTypeName = "default"
)

func TestPrepareCreateRequest(t *testing.T) {
	functionUnderTest := "PrepareCreateRequest"

	zonesForSCMultiZoneTestCase := "nova1, nova2, nova3"
	setOfZonesForSCMultiZoneTestCase, _ := util.ZonesToSet(zonesForSCMultiZoneTestCase)
	pvcNameForSCMultiZoneTestCase := "pvc"
	expectedResultForSCMultiZoneTestCase := volume.ChooseZoneForVolume(setOfZonesForSCMultiZoneTestCase, pvcNameForSCMultiZoneTestCase)
	pvcNameForSCNoZonesSpecifiedTestCase := "pvc"
	expectedResultForSCNoZonesSpecifiedTestCase := "nova"
	succCaseStorageSize, _ := resource.ParseQuantity("2G")
	// First part: want no error
	succCases := []struct {
		volumeOptions controller.VolumeOptions
		want          shares.CreateOpts
	}{
		// Will very probably start failing if the func volume.ChooseZoneForVolume is replaced by another function in the implementation
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcNameForSCNoZonesSpecifiedTestCase,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: succCaseStorageSize,
							},
						},
					},
				},
				Parameters: map[string]string{},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: expectedResultForSCNoZonesSpecifiedTestCase,
				Size:             2,
			},
		},
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: succCaseStorageSize,
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: fakeZoneName,
				Size:             2,
			},
		},
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: succCaseStorageSize,
							},
						},
					},
				},
				Parameters: map[string]string{"ZoNes": fakeZoneName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: fakeZoneName,
				Size:             2,
			},
		},
		// Will very probably start failing if the func volume.ChooseZoneForVolume is replaced by another function in the implementation
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcNameForSCMultiZoneTestCase,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: succCaseStorageSize,
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: zonesForSCMultiZoneTestCase},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: expectedResultForSCMultiZoneTestCase,
				Size:             2,
			},
		},
		// PVC accessModes parameters are being ignored.
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadOnlyMany},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: succCaseStorageSize,
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: fakeZoneName,
				Size:             2,
			},
		},
		// In case the requested storage size in GB is not a whole number because of the chosen units the storage size in GB is rounded up to the nearest integer
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2Gi"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: fakeZoneName,
				Size:             3,
			},
		},
		// In case the requested storage size is not a whole number the storage size is rounded up to the nearest integer
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2.2G"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				AvailabilityZone: fakeZoneName,
				Size:             3,
			},
		},
		// Optional parameter "type" is present in the Storage Class
		{
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2.2G"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName, TypeSCParamName: fakeShareTypeName},
			},
			want: shares.CreateOpts{
				ShareProto:       ProtocolNFS,
				Name:             fakeShareName,
				ShareType:        fakeShareTypeName,
				AvailabilityZone: fakeZoneName,
				Size:             3,
			},
		},
	}
	for i, succCase := range succCases {
		tags := make(map[string]string)
		tags[persistentvolume.CloudVolumeCreatedForClaimNamespaceTag] = fakeNamespace
		tags[persistentvolume.CloudVolumeCreatedForClaimNameTag] = succCase.volumeOptions.PVC.Name
		tags[persistentvolume.CloudVolumeCreatedForVolumeNameTag] = succCase.want.Name
		succCase.want.Metadata = tags
		if request, err := PrepareCreateRequest(succCase.volumeOptions); err != nil {
			t.Errorf("Test case %v: %v(%v) RETURNED (%v, %v), WANT (%v, %v)", i, functionUnderTest, succCase.volumeOptions, request, err, succCase.want, nil)
		} else if !reflect.DeepEqual(request, succCase.want) {
			t.Errorf("Test case %v: %v(%v) RETURNED (%v, %v), WANT (%v, %v)", i, functionUnderTest, succCase.volumeOptions, request, err, succCase.want, nil)
		}
	}

	// Second part: want an error
	errCases := []struct {
		testCaseName  string
		volumeOptions controller.VolumeOptions
	}{
		{
			testCaseName: "unknown Storage Class option",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "foo"},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2G"),
							},
						},
					},
				},
				Parameters: map[string]string{"foo": "bar"},
			},
		},
		{
			testCaseName: "zero storage capacity",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "foo"},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("0G"),
							},
						},
					},
				},
				Parameters: map[string]string{},
			},
		},
		{
			testCaseName: "negative storage capacity",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "foo"},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("-1G"),
							},
						},
					},
				},
				Parameters: map[string]string{},
			},
		},
		{
			testCaseName: "storage size not configured 1",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "foo"},
					Spec:       v1.PersistentVolumeClaimSpec{},
				},
				Parameters: map[string]string{},
			},
		},
		{
			testCaseName: "storage size not configured 2",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "foo"},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.Quantity{},
							},
						},
					},
				},
				Parameters: map[string]string{},
			},
		},
	}
	for _, errCase := range errCases {
		if request, err := PrepareCreateRequest(errCase.volumeOptions); err == nil {
			t.Errorf("Test case %q: %v(%v) RETURNED (%v, %v), WANT (%v, %v)", errCase.testCaseName, functionUnderTest, errCase.volumeOptions, request, err, "N/A", "an error")
		}
	}
}

const (
	validPath              = "ip://directory"
	preferredPath          = "ip://preferred/directory"
	emptyPath              = ""
	spacesOnlyPath         = "  	  "
	shareExportLocationID1 = "123456-1"
	shareExportLocationID2 = "1234567-1"
	shareExportLocationID3 = "1234567-2"
	shareExportLocationID4 = "7654321-1"
	shareID1               = "123456"
	shareID2               = "1234567"
)

func TestChooseExportLocationSuccess(t *testing.T) {
	tests := []struct {
		testCaseName string
		locs         []shares.ExportLocation
		want         shares.ExportLocation
	}{
		{
			testCaseName: "Match first item:",
			locs: []shares.ExportLocation{
				{
					Path:            validPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     false,
					ID:              shareExportLocationID1,
					Preferred:       false,
				},
			},
			want: shares.ExportLocation{
				Path:            validPath,
				ShareInstanceID: shareID1,
				IsAdminOnly:     false,
				ID:              shareExportLocationID1,
				Preferred:       false,
			},
		},
		{
			testCaseName: "Match preferred location:",
			locs: []shares.ExportLocation{
				{
					Path:            validPath,
					ShareInstanceID: shareID2,
					IsAdminOnly:     false,
					ID:              shareExportLocationID2,
					Preferred:       false,
				},
				{
					Path:            preferredPath,
					ShareInstanceID: shareID2,
					IsAdminOnly:     false,
					ID:              shareExportLocationID3,
					Preferred:       true,
				},
			},
			want: shares.ExportLocation{
				Path:            preferredPath,
				ShareInstanceID: shareID2,
				IsAdminOnly:     false,
				ID:              shareExportLocationID3,
				Preferred:       true,
			},
		},
		{
			testCaseName: "Match first not-preferred location that matches shareID:",
			locs: []shares.ExportLocation{
				{
					Path:            validPath,
					ShareInstanceID: shareID2,
					IsAdminOnly:     false,
					ID:              shareExportLocationID2,
					Preferred:       false,
				},
				{
					Path:            preferredPath,
					ShareInstanceID: shareID2,
					IsAdminOnly:     false,
					ID:              shareExportLocationID3,
					Preferred:       false,
				},
			},
			want: shares.ExportLocation{
				Path:            validPath,
				ShareInstanceID: shareID2,
				IsAdminOnly:     false,
				ID:              shareExportLocationID2,
				Preferred:       false,
			},
		},
	}

	for _, tt := range tests {
		if got, err := ChooseExportLocation(tt.locs); err != nil {
			t.Errorf("%q ChooseExportLocation(%v) = (%v, %q) want (%v, nil)", tt.testCaseName, tt.locs, got, err.Error(), tt.want)
		} else if !reflect.DeepEqual(tt.want, got) {
			t.Errorf("%q ChooseExportLocation(%v) = (%v, nil) want (%v, nil)", tt.testCaseName, tt.locs, got, tt.want)
		}
	}
}

func TestChooseExportLocationNotFound(t *testing.T) {
	tests := []struct {
		testCaseName string
		locs         []shares.ExportLocation
	}{
		{
			testCaseName: "Empty slice:",
			locs:         []shares.ExportLocation{},
		},
		{
			testCaseName: "Locations for admins only:",
			locs: []shares.ExportLocation{
				{
					Path:            validPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     true,
					ID:              shareExportLocationID1,
					Preferred:       false,
				},
			},
		},
		{
			testCaseName: "Preferred locations for admins only:",
			locs: []shares.ExportLocation{
				{
					Path:            validPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     true,
					ID:              shareExportLocationID1,
					Preferred:       true,
				},
			},
		},
		{
			testCaseName: "Empty path:",
			locs: []shares.ExportLocation{
				{
					Path:            emptyPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     false,
					ID:              shareExportLocationID1,
					Preferred:       false,
				},
			},
		},
		{
			testCaseName: "Empty path in preferred location:",
			locs: []shares.ExportLocation{
				{
					Path:            emptyPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     false,
					ID:              shareExportLocationID1,
					Preferred:       true,
				},
			},
		},
		{
			testCaseName: "Path containing spaces only:",
			locs: []shares.ExportLocation{
				{
					Path:            spacesOnlyPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     false,
					ID:              shareExportLocationID1,
					Preferred:       false,
				},
			},
		},
		{
			testCaseName: "Preferred path containing spaces only:",
			locs: []shares.ExportLocation{
				{
					Path:            spacesOnlyPath,
					ShareInstanceID: shareID1,
					IsAdminOnly:     false,
					ID:              shareExportLocationID1,
					Preferred:       true,
				},
			},
		},
	}
	for _, tt := range tests {
		if got, err := ChooseExportLocation(tt.locs); err == nil {
			t.Errorf("%q ChooseExportLocation(%v) = (%v, nil) want (\"N/A\", \"an error\")", tt.testCaseName, tt.locs, got)
		}
	}
}

func TestFillInPV(t *testing.T) {
	functionUnderTest := "FillInPV"
	tests := []struct {
		testCaseName   string
		volumeOptions  controller.VolumeOptions
		share          shares.Share
		exportLocation shares.ExportLocation
		want           *v1.PersistentVolume
	}{
		{
			testCaseName: "Storage size in GB is a whole number",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2G"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			share: shares.Share{
				AvailabilityZone:   fakeZoneName,
				Description:        "",
				DisplayDescription: "",
				DisplayName:        "",
				HasReplicas:        false,
				Host:               "",
				ID:                 fakeShareID,
				IsPublic:           false,
				Links: []map[string]string{
					{"href:http://controller:8786/v2/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:self"},
					{"href:http://controller:8786/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:bookmark"},
				},
				Metadata:                 map[string]string{persistentvolume.CloudVolumeCreatedForClaimNamespaceTag: fakeNamespace, persistentvolume.CloudVolumeCreatedForClaimNameTag: fakePVCName, persistentvolume.CloudVolumeCreatedForVolumeNameTag: fakeShareName},
				Name:                     fakeShareName,
				ProjectID:                "ecbc0da9369f41e3a8a17e49a425ff2d",
				ReplicationType:          "",
				ShareNetworkID:           "",
				ShareProto:               ProtocolNFS,
				ShareServerID:            "",
				ShareType:                "e60e2fa9-d2e8-4772-b24b-c45a54e05e53",
				ShareTypeName:            "default_share_type",
				Size:                     2,
				SnapshotID:               "",
				Status:                   "creating",
				TaskState:                "",
				VolumeType:               "default_share_type",
				ConsistencyGroupID:       "",
				SnapshotSupport:          true,
				SourceCgsnapshotMemberID: "",
				CreatedAt:                time.Date(2015, time.August, 27, 11, 33, 21, 0, time.UTC),
			},
			exportLocation: shares.ExportLocation{
				Path:            "127.0.0.1:/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
				ShareInstanceID: "",
				IsAdminOnly:     false,
				ID:              "68e3aeb3-0c8f-4a55-804f-ee2d26ebf814",
				Preferred:       false,
			},
			want: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
					Annotations: map[string]string{
						ManilaAnnotationShareIDName: fakeShareID,
					},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
						v1.ReadOnlyMany,
						v1.ReadWriteMany,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("2G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
		},
		{
			testCaseName: "Storage size in GB must be rounded up",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2Gi"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			share: shares.Share{
				AvailabilityZone:   fakeZoneName,
				Description:        "",
				DisplayDescription: "",
				DisplayName:        "",
				HasReplicas:        false,
				Host:               "",
				ID:                 fakeShareID,
				IsPublic:           false,
				Links: []map[string]string{
					{"href:http://controller:8786/v2/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:self"},
					{"href:http://controller:8786/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:bookmark"},
				},
				Metadata:                 map[string]string{persistentvolume.CloudVolumeCreatedForClaimNamespaceTag: fakeNamespace, persistentvolume.CloudVolumeCreatedForClaimNameTag: fakePVCName, persistentvolume.CloudVolumeCreatedForVolumeNameTag: fakeShareName},
				Name:                     fakeShareName,
				ProjectID:                "ecbc0da9369f41e3a8a17e49a425ff2d",
				ReplicationType:          "",
				ShareNetworkID:           "",
				ShareProto:               ProtocolNFS,
				ShareServerID:            "",
				ShareType:                "e60e2fa9-d2e8-4772-b24b-c45a54e05e53",
				ShareTypeName:            "default_share_type",
				Size:                     3,
				SnapshotID:               "",
				Status:                   "creating",
				TaskState:                "",
				VolumeType:               "default_share_type",
				ConsistencyGroupID:       "",
				SnapshotSupport:          true,
				SourceCgsnapshotMemberID: "",
				CreatedAt:                time.Date(2015, time.August, 27, 11, 33, 21, 0, time.UTC),
			},
			exportLocation: shares.ExportLocation{
				Path:            "127.0.0.1:/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
				ShareInstanceID: "",
				IsAdminOnly:     false,
				ID:              "68e3aeb3-0c8f-4a55-804f-ee2d26ebf814",
				Preferred:       false,
			},
			want: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
					Annotations: map[string]string{
						ManilaAnnotationShareIDName: fakeShareID,
					},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
						v1.ReadOnlyMany,
						v1.ReadWriteMany,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("3G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
		},
		{
			testCaseName: "Access Mode is configured in PVC",
			volumeOptions: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
				PVName: fakePVName,
				PVC: &v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fakePVCName,
						Namespace: fakeNamespace,
						UID:       fakeUID},
					Spec: v1.PersistentVolumeClaimSpec{
						AccessModes: []v1.PersistentVolumeAccessMode{
							v1.ReadOnlyMany,
							v1.ReadWriteOnce,
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceStorage: resource.MustParse("2G"),
							},
						},
					},
				},
				Parameters: map[string]string{ZonesSCParamName: fakeZoneName},
			},
			share: shares.Share{
				AvailabilityZone:   fakeZoneName,
				Description:        "",
				DisplayDescription: "",
				DisplayName:        "",
				HasReplicas:        false,
				Host:               "",
				ID:                 fakeShareID,
				IsPublic:           false,
				Links: []map[string]string{
					{"href:http://controller:8786/v2/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:self"},
					{"href:http://controller:8786/ecbc0da9369f41e3a8a17e49a425ff2d/shares/de64eb77-05cb-4502-a6e5-7e8552c352f3": "rel:bookmark"},
				},
				Metadata:                 map[string]string{persistentvolume.CloudVolumeCreatedForClaimNamespaceTag: fakeNamespace, persistentvolume.CloudVolumeCreatedForClaimNameTag: fakePVCName, persistentvolume.CloudVolumeCreatedForVolumeNameTag: fakeShareName},
				Name:                     fakeShareName,
				ProjectID:                "ecbc0da9369f41e3a8a17e49a425ff2d",
				ReplicationType:          "",
				ShareNetworkID:           "",
				ShareProto:               ProtocolNFS,
				ShareServerID:            "",
				ShareType:                "e60e2fa9-d2e8-4772-b24b-c45a54e05e53",
				ShareTypeName:            "default_share_type",
				Size:                     2,
				SnapshotID:               "",
				Status:                   "creating",
				TaskState:                "",
				VolumeType:               "default_share_type",
				ConsistencyGroupID:       "",
				SnapshotSupport:          true,
				SourceCgsnapshotMemberID: "",
				CreatedAt:                time.Date(2015, time.August, 27, 11, 33, 21, 0, time.UTC),
			},
			exportLocation: shares.ExportLocation{
				Path:            "127.0.0.1:/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
				ShareInstanceID: "",
				IsAdminOnly:     false,
				ID:              "68e3aeb3-0c8f-4a55-804f-ee2d26ebf814",
				Preferred:       false,
			},
			want: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
					Annotations: map[string]string{
						ManilaAnnotationShareIDName: fakeShareID,
					},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadOnlyMany,
						v1.ReadWriteOnce,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("2G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		if got, err := FillInPV(tt.volumeOptions, tt.share, tt.exportLocation); err != nil {
			t.Errorf("Test case: %q; %v(%v, %v, %v) = (%v, %v) WANT (%v, nil)", tt.testCaseName, functionUnderTest, tt.volumeOptions, tt.share, tt.exportLocation, got, err.Error(), tt.want)
		} else if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Test case: %q; %v(%v, %v, %v) = (%v, %v) WANT (%v, nil)", tt.testCaseName, functionUnderTest, tt.volumeOptions, tt.share, tt.exportLocation, got, err, tt.want)
		}
	}
}

func TestGetServerAndPath(t *testing.T) {
	functionUnderTest := "getServerAndPath"
	// want success
	exportLocationPath := "10.0.0.2:/var/lib/manila-shares/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00"
	wantServer := "10.0.0.2"
	wantPath := "/var/lib/manila-shares/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00"
	if gotServer, gotPath, err := getServerAndPath(exportLocationPath); err != nil {
		t.Errorf("%v(%q) = (%q, %q, %q) WANT (%q, %q, nil)", functionUnderTest, exportLocationPath, gotServer, gotPath, err.Error(), wantServer, wantPath)
	} else if gotServer != wantServer || gotPath != wantPath {
		t.Errorf("%v(%q) = (%q, %q, %q) WANT (%q, %q, nil)", functionUnderTest, exportLocationPath, gotServer, gotPath, err, wantServer, wantPath)
	}
	// want an error
	exportLocationPath = "127.0.0.1string/without/colon"
	if gotServer, gotPath, err := getServerAndPath(exportLocationPath); err == nil {
		t.Errorf("%v(%q) = (%q, %q, %q) WANT (\"\", \"\", \"an error\")", functionUnderTest, exportLocationPath, gotServer, gotPath, err)
	}
}

func TestGetShareIDfromPV(t *testing.T) {
	functionUnderTest := "GetShareIDfromPV"
	// want success
	succCases := []struct {
		volume *v1.PersistentVolume
		want   string
	}{
		{
			volume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
					Annotations: map[string]string{
						ManilaAnnotationShareIDName: fakeShareID,
					},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
						v1.ReadOnlyMany,
						v1.ReadWriteMany,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("3G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
			want: fakeShareID,
		},
	}
	for _, succCase := range succCases {
		if got, err := GetShareIDfromPV(succCase.volume); err != nil {
			t.Errorf("%v(%v) = (%q, %q) WANT (%q, nil)", functionUnderTest, succCase.volume, got, err.Error(), succCase.want)
		} else if got != succCase.want {
			t.Errorf("%v(%v) = (%q, nil) WANT (%q, nil)", functionUnderTest, succCase.volume, got, succCase.want)
		}
	}

	// want an error
	errCases := []struct {
		testCaseName string
		volume       *v1.PersistentVolume
	}{
		{
			testCaseName: "Empty Annotations field",
			volume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
						v1.ReadOnlyMany,
						v1.ReadWriteMany,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("3G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
		},
		{
			testCaseName: ManilaAnnotationShareIDName + " key doesn't exist in Annotations",
			volume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: fakePVName,
					Annotations: map[string]string{
						"foo": "bar",
					},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: fakeReclaimPolicy,
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
						v1.ReadOnlyMany,
						v1.ReadWriteMany,
					},
					Capacity: v1.ResourceList{
						v1.ResourceStorage: resource.MustParse("3G"),
					},
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "127.0.0.1",
							Path:     "/var/lib/manila/mnt/share-0ee809f3-edd3-4603-973a-8049311f8d00",
							ReadOnly: false,
						},
					},
				},
			},
		},
	}
	for _, errCase := range errCases {
		if got, err := GetShareIDfromPV(errCase.volume); err == nil {
			t.Errorf("%q: %v(%v) = (%q, nil) WANT (\"any result\", \"an error\")", errCase.testCaseName, functionUnderTest, errCase.volume, got)
		}
	}

}
