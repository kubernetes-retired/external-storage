/*
Copyright 2016 The Kubernetes Authors.

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
	"errors"
	"reflect"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/api/resource"
	"k8s.io/client-go/pkg/api/testapi"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/storage/v1beta1"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/types"
	testclient "k8s.io/client-go/testing"
)

func TestController(t *testing.T) {
	tests := []struct {
		name            string
		objs            []runtime.Object
		provisionerName string
		provisioner     Provisioner
		verbs           []string
		reaction        testclient.ReactionFunc
		expectedVolumes []v1.PersistentVolume
	}{
		{
			name: "provision for claim-1 but not claim-2",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newStorageClass("class-2", "abc.def/ghi"),
				newClaim("claim-1", "uid-1-1", "class-1", "", nil),
				newClaim("claim-2", "uid-1-2", "class-2", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume{
				*newProvisionedVolume(newStorageClass("class-1", "foo.bar/baz"), newClaim("claim-1", "uid-1-1", "class-1", "", nil)),
			},
		},
		{
			name: "delete volume-1 but not volume-2",
			objs: []runtime.Object{
				newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
				newVolume("volume-2", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "abc.def/ghi"}),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume{
				*newVolume("volume-2", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "abc.def/ghi"}),
			},
		},
		{
			name: "don't provision for claim-1 because it's already bound",
			objs: []runtime.Object{
				newClaim("claim-1", "uid-1-1", "class-1", "volume-1", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume(nil),
		},
		{
			name: "don't provision for claim-1 because its class doesn't exist",
			objs: []runtime.Object{
				newClaim("claim-1", "uid-1-1", "class-1", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume(nil),
		},
		{
			name: "don't delete volume-1 because it's still bound",
			objs: []runtime.Object{
				newVolume("volume-1", v1.VolumeBound, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume{
				*newVolume("volume-1", v1.VolumeBound, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
		},
		{
			name: "don't delete volume-1 because its reclaim policy is not delete",
			objs: []runtime.Object{
				newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimRetain, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume{
				*newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimRetain, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
		},
		{
			name: "provisioner fails to provision for claim-1: no pv is created",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newClaim("claim-1", "uid-1-1", "class-1", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newBadTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume(nil),
		},
		{
			name: "provisioner fails to delete volume-1: pv is not deleted",
			objs: []runtime.Object{
				newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newBadTestProvisioner(),
			expectedVolumes: []v1.PersistentVolume{
				*newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
		},
		{
			name: "try to provision for claim-1 but fail to save the pv object",
			objs: []runtime.Object{
				newStorageClass("class-1", "foo.bar/baz"),
				newClaim("claim-1", "uid-1-1", "class-1", "", nil),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			verbs:           []string{"create"},
			reaction: func(action testclient.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, errors.New("fake error")
			},
			expectedVolumes: []v1.PersistentVolume(nil),
		},
		{
			name: "try to delete volume-1 but fail to delete the pv object",
			objs: []runtime.Object{
				newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
			provisionerName: "foo.bar/baz",
			provisioner:     newTestProvisioner(),
			verbs:           []string{"delete"},
			reaction: func(action testclient.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, errors.New("fake error")
			},
			expectedVolumes: []v1.PersistentVolume{
				*newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			},
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset(test.objs...)
		if len(test.verbs) != 0 {
			for _, v := range test.verbs {
				client.Fake.PrependReactor(v, "persistentvolumes", test.reaction)
			}
		}
		resyncPeriod := 100 * time.Millisecond
		ctrl := NewProvisionController(client, resyncPeriod, test.provisionerName, test.provisioner, "v1.5.0", 5, 10*time.Millisecond, false)

		stopCh := make(chan struct{})
		go ctrl.Run(stopCh)

		time.Sleep(2 * resyncPeriod)
		ctrl.runningOperations.Wait()

		pvList, _ := client.Core().PersistentVolumes().List(v1.ListOptions{})
		if !reflect.DeepEqual(test.expectedVolumes, pvList.Items) {
			t.Logf("test case: %s", test.name)
			t.Errorf("expected PVs:\n %v\n but got:\n %v\n", test.expectedVolumes, pvList.Items)
		}
		close(stopCh)
	}
}

func TestShouldProvision(t *testing.T) {
	tests := []struct {
		name            string
		provisionerName string
		class           *v1beta1.StorageClass
		claim           *v1.PersistentVolumeClaim
		expectedShould  bool
	}{
		{
			name:            "should provision",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-1", "foo.bar/baz"),
			claim:           newClaim("claim-1", "1-1", "class-1", "", nil),
			expectedShould:  true,
		},
		{
			name:            "claim already bound",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-1", "foo.bar/baz"),
			claim:           newClaim("claim-1", "1-1", "class-1", "foo", nil),
			expectedShould:  false,
		},
		{
			name:            "no such class",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-1", "foo.bar/baz"),
			claim:           newClaim("claim-1", "1-1", "class-2", "", nil),
			expectedShould:  false,
		},
		{
			name:            "not this provisioner's job",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-1", "abc.def/ghi"),
			claim:           newClaim("claim-1", "1-1", "class-1", "", nil),
			expectedShould:  false,
		},
		// Kubernetes 1.5 provisioning - annDynamicallyProvisioned is set
		// and only this annotation is evaluated
		{
			name:            "should provision 1.5",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-2", "abc.def/ghi"),
			claim: newClaim("claim-1", "1-1", "class-1", "",
				map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			expectedShould: true,
		},
		{
			name:            "unknown provisioner 1.5",
			provisionerName: "foo.bar/baz",
			class:           newStorageClass("class-1", "foo.bar/baz"),
			claim: newClaim("claim-1", "1-1", "class-1", "",
				map[string]string{annDynamicallyProvisioned: "abc.def/ghi"}),
			expectedShould: false,
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset(test.claim)
		resyncPeriod := 100 * time.Millisecond
		provisioner := newTestProvisioner()
		ctrl := NewProvisionController(client, resyncPeriod, test.provisionerName, provisioner, "v1.5.0", 5, 10*time.Second, false)

		err := ctrl.classes.Add(test.class)
		if err != nil {
			t.Logf("test case: %s", test.name)
			t.Errorf("error adding class %v to cache: %v", test.class, err)
		}

		should := ctrl.shouldProvision(test.claim)
		if test.expectedShould != should {
			t.Logf("test case: %s", test.name)
			t.Errorf("expected should provision %v but got %v\n", test.expectedShould, should)
		}
	}
}

func TestShouldDelete(t *testing.T) {
	tests := []struct {
		name             string
		provisionerName  string
		volume           *v1.PersistentVolume
		serverGitVersion string
		expectedShould   bool
	}{
		{
			name:             "should delete",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			serverGitVersion: "v1.5.0",
			expectedShould:   true,
		},
		{
			name:             "1.4 and failed: should delete",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeFailed, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			serverGitVersion: "v1.4.0",
			expectedShould:   true,
		},
		{
			name:             "1.5 and failed: shouldn't delete",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeFailed, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			serverGitVersion: "v1.5.0",
			expectedShould:   false,
		},
		{
			name:             "volume still bound",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeBound, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			serverGitVersion: "v1.5.0",
			expectedShould:   false,
		},
		{
			name:             "non-delete reclaim policy",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimRetain, map[string]string{annDynamicallyProvisioned: "foo.bar/baz"}),
			serverGitVersion: "v1.5.0",
			expectedShould:   false,
		},
		{
			name:             "not this provisioner's job",
			provisionerName:  "foo.bar/baz",
			volume:           newVolume("volume-1", v1.VolumeReleased, v1.PersistentVolumeReclaimDelete, map[string]string{annDynamicallyProvisioned: "abc.def/ghi"}),
			serverGitVersion: "v1.5.0",
			expectedShould:   false,
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset()
		resyncPeriod := 100 * time.Millisecond
		provisioner := newTestProvisioner()
		ctrl := NewProvisionController(client, resyncPeriod, test.provisionerName, provisioner, test.serverGitVersion, 5, 10*time.Second, false)

		should := ctrl.shouldDelete(test.volume)
		if test.expectedShould != should {
			t.Logf("test case: %s", test.name)
			t.Errorf("expected should delete %v but got %v\n", test.expectedShould, should)
		}
	}
}

func newStorageClass(name, provisioner string) *v1beta1.StorageClass {
	return &v1beta1.StorageClass{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Provisioner: provisioner,
	}
}

func newClaim(name, claimUID, provisioner, volumeName string, annotations map[string]string) *v1.PersistentVolumeClaim {
	claim := &v1.PersistentVolumeClaim{
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
			VolumeName: volumeName,
		},
		Status: v1.PersistentVolumeClaimStatus{
			Phase: v1.ClaimPending,
		},
	}
	for k, v := range annotations {
		claim.Annotations[k] = v
	}
	return claim
}

func newVolume(name string, phase v1.PersistentVolumePhase, policy v1.PersistentVolumeReclaimPolicy, annotations map[string]string) *v1.PersistentVolume {
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: policy,
			AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   "foo",
					Path:     "bar",
					ReadOnly: false,
				},
			},
		},
		Status: v1.PersistentVolumeStatus{
			Phase: phase,
		},
	}

	return pv
}

// newProvisionedVolume returns the volume the test controller should provision for the
// given claim with the given class
func newProvisionedVolume(storageClass *v1beta1.StorageClass, claim *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	// pv.Spec MUST be set to match requirements in claim.Spec, especially access mode and PV size. The provisioned volume size MUST NOT be smaller than size requested in the claim, however it MAY be larger.
	options := VolumeOptions{
		Capacity:                      claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
		AccessModes:                   claim.Spec.AccessModes,
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
		PVName:     "pvc-" + string(claim.ObjectMeta.UID),
		Parameters: storageClass.Parameters,
	}
	volume, _ := newTestProvisioner().Provision(options)

	// pv.Spec.ClaimRef MUST point to the claim that led to its creation (including the claim UID).
	volume.Spec.ClaimRef, _ = v1.GetReference(claim)

	// pv.Annotations["pv.kubernetes.io/provisioned-by"] MUST be set to name of the external provisioner. This provisioner will be used to delete the volume.
	// pv.Annotations["volume.beta.kubernetes.io/storage-class"] MUST be set to name of the storage class requested by the claim.
	volume.Annotations = map[string]string{annDynamicallyProvisioned: storageClass.Provisioner, annClass: storageClass.Name}

	// TODO implement options.ProvisionerSelector parsing
	// pv.Labels MUST be set to match claim.spec.selector. The provisioner MAY add additional labels.

	return volume
}

func newTestProvisioner() Provisioner {
	return &testProvisioner{}
}

type testProvisioner struct {
}

var _ Provisioner = &testProvisioner{}

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

func newBadTestProvisioner() Provisioner {
	return &badTestProvisioner{}
}

type badTestProvisioner struct {
}

var _ Provisioner = &badTestProvisioner{}

func (p *badTestProvisioner) Provision(options VolumeOptions) (*v1.PersistentVolume, error) {
	return nil, errors.New("fake error")
}

func (p *badTestProvisioner) Delete(volume *v1.PersistentVolume) error {
	return errors.New("fake error")
}
