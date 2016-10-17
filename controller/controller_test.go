/*
Copyright 2016 Red Hat, Inc.

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

package controller

import (
	"k8s.io/client-go/1.4/kubernetes/fake"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/testapi"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/apis/storage/v1beta1"
	"k8s.io/client-go/1.4/pkg/runtime"
	"k8s.io/client-go/1.4/pkg/types"

	// "flag"
	"reflect"
	"testing"
	"time"
)

func init() {
	// flag.Set("v", "5")
	// flag.Set("alsologtostderr", "true")
}

func TestController(t *testing.T) {
	// The (only) provisioner for which the controller should provision volumes
	provisionerName := "foo"

	tests := []struct {
		objs            []runtime.Object
		expectedVolumes []v1.PersistentVolume
	}{
		// 2 classes, 1 claim each
		{
			objs: []runtime.Object{
				newStorageClass("class-1", provisionerName),
				newStorageClass("class-2", "bar"),
				newClaim("claim-1", "uid-1-1", "class-1"),
				newClaim("claim-2", "uid-1-2", "class-2"),
			},
			expectedVolumes: []v1.PersistentVolume{
				*newVolume(newStorageClass("class-1", provisionerName), newClaim("claim-1", "uid-1-1", "class-1")),
			},
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset(test.objs...)
		resyncPeriod := 1 * time.Second
		provisioner := newTestProvisioner()

		ctrl := NewProvisionController(client, resyncPeriod, provisionerName, provisioner)
		ctrl.createProvisionedPVInterval = 10 * time.Millisecond
		stopCh := make(chan struct{})
		go ctrl.Run(stopCh)

		time.Sleep(2 * resyncPeriod)
		ctrl.runningOperations.Wait()

		pvList, _ := client.Core().PersistentVolumes().List(api.ListOptions{})
		if !reflect.DeepEqual(test.expectedVolumes, pvList.Items) {
			t.Errorf("Expected PVs:\n %v\n but got:\n %v\n", test.expectedVolumes, pvList.Items)
		}
		close(stopCh)
	}
}

func newStorageClass(name string, provisioner string) *v1beta1.StorageClass {
	return &v1beta1.StorageClass{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Provisioner: provisioner,
	}
}

func newClaim(name, claimUID, provisioner string) *v1.PersistentVolumeClaim {
	return &v1.PersistentVolumeClaim{
		ObjectMeta: v1.ObjectMeta{
			Name:            name,
			Namespace:       "default",
			UID:             types.UID(claimUID),
			ResourceVersion: "1",
			Annotations:     map[string]string{annClass: provisioner},
			SelfLink:        testapi.Default.SelfLink("pvc", ""),
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
				},
			},
		},
		Status: v1.PersistentVolumeClaimStatus{
			Phase: v1.ClaimPending,
		},
	}
}

// newVolume returns the volume the test controller should provision for the
// given claim with the given class
func newVolume(storageClass *v1beta1.StorageClass, claim *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	options := VolumeOptions{
		Capacity:                      claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
		AccessModes:                   claim.Spec.AccessModes,
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
		PVName:     "pvc-" + string(claim.ObjectMeta.UID),
		Parameters: storageClass.Parameters,
	}
	volume, _ := newTestProvisioner().Provision(options)
	volume.Spec.ClaimRef, _ = v1.GetReference(claim)
	volume.Annotations = map[string]string{annDynamicallyProvisioned: storageClass.Provisioner, annClass: storageClass.Name}
	return volume
}

func newTestProvisioner() Provisioner {
	return &testProvisioner{}
}

type testProvisioner struct {
}

func (p *testProvisioner) Provision(options VolumeOptions) (*v1.PersistentVolume, error) {
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.Capacity,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   "foo",
					Path:     "bar",
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

func (p *testProvisioner) Delete(volume *v1.PersistentVolume) error {
	return nil
}

func (p *testProvisioner) Exists(volume *v1.PersistentVolume) bool {
	return true
}
