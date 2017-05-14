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

package deleter

import (
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/types"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	testHostDir  = "/mnt/disks"
	testMountDir = "/discoveryPath"
	testNode     = "test-node"
)

type testConfig struct {
	apiShouldFail bool
	// Precreated PVs
	vols map[string]*testVol
	// Expected names of deleted PV
	expectedDeletedPVs map[string]string
	// These two interfaces are set during setup
	volUtil *util.FakeVolumeUtil
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

type testVol struct {
	pvPhase v1.PersistentVolumePhase
}

func TestDeleteVolumes_Basic(t *testing.T) {
	vols := map[string]*testVol{
		"pv1": &testVol{
			pvPhase: v1.VolumePending,
		},
		"pv2": &testVol{
			pvPhase: v1.VolumeAvailable,
		},
		"pv3": &testVol{
			pvPhase: v1.VolumeBound,
		},
		"pv4": &testVol{
			pvPhase: v1.VolumeReleased,
		},
		"pv5": &testVol{
			pvPhase: v1.VolumeFailed,
		},
	}
	expectedDeletedPVs := map[string]string{"pv4": ""}
	test := &testConfig{
		vols:               vols,
		expectedDeletedPVs: expectedDeletedPVs,
	}
	d := testSetup(t, test)

	d.DeletePVs()
	verifyDeletedPVs(t, test)
}

func TestDeleteVolumes_Twice(t *testing.T) {
	vols := map[string]*testVol{
		"pv4": &testVol{
			pvPhase: v1.VolumeReleased,
		},
	}
	expectedDeletedPVs := map[string]string{"pv4": ""}
	test := &testConfig{
		vols:               vols,
		expectedDeletedPVs: expectedDeletedPVs,
	}
	d := testSetup(t, test)

	d.DeletePVs()
	verifyDeletedPVs(t, test)

	d.DeletePVs()
	test.expectedDeletedPVs = map[string]string{}
	verifyDeletedPVs(t, test)
}

func TestDeleteVolumes_Empty(t *testing.T) {
	vols := map[string]*testVol{}
	expectedDeletedPVs := map[string]string{}
	test := &testConfig{
		vols:               vols,
		expectedDeletedPVs: expectedDeletedPVs,
	}
	d := testSetup(t, test)

	d.DeletePVs()
	verifyDeletedPVs(t, test)
}

func TestDeleteVolumes_DeletePVFails(t *testing.T) {
	vols := map[string]*testVol{
		"pv4": &testVol{
			pvPhase: v1.VolumeReleased,
		},
	}
	test := &testConfig{
		apiShouldFail:      true,
		vols:               vols,
		expectedDeletedPVs: map[string]string{},
	}
	d := testSetup(t, test)

	d.DeletePVs()
	verifyDeletedPVs(t, test)
	verifyPVExists(t, test)
}

func testSetup(t *testing.T, config *testConfig) Deleter {
	config.volUtil = util.NewFakeVolumeUtil()
	config.apiUtil = util.NewFakeAPIUtil(false)
	config.cache = cache.NewVolumeCache()

	// Precreate PVs
	for pvName, vol := range config.vols {
		pv := &v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: pvName,
			},
			Status: v1.PersistentVolumeStatus{
				Phase: vol.pvPhase,
			},
		}
		_, err := config.apiUtil.CreatePV(pv)
		if err != nil {
			t.Fatalf("Error creating fake PV: %v", err)
		}
		err = config.cache.AddPV(pv)
		if err != nil {
			t.Fatalf("Error adding PV to cache: %v", err)
		}
	}

	config.apiUtil = util.NewFakeAPIUtil(config.apiShouldFail)
	userConfig := &types.UserConfig{
		NodeName: testNode,
		MountDir: testMountDir,
		HostDir:  testHostDir,
	}
	runtimeConfig := &types.RuntimeConfig{
		UserConfig: userConfig,
		Cache:      config.cache,
		VolUtil:    config.volUtil,
		APIUtil:    config.apiUtil,
	}
	return NewDeleter(runtimeConfig)
}

func verifyDeletedPVs(t *testing.T, config *testConfig) {
	deletedPVs := config.apiUtil.GetAndResetDeletedPVs()
	expectedLen := len(config.expectedDeletedPVs)
	actualLen := len(deletedPVs)
	if expectedLen != actualLen {
		t.Errorf("Expected %v deleted PVs, got %v", expectedLen, actualLen)
	}

	for pvName := range deletedPVs {
		_, found := config.expectedDeletedPVs[pvName]
		if !found {
			t.Errorf("Did not expect deleted PVs %v", pvName)
			continue
		}
		if config.cache.PVExists(pvName) {
			t.Errorf("PV %q still exists in cache", pvName)
		}
	}
}

func verifyPVExists(t *testing.T, config *testConfig) {
	for pvName := range config.vols {
		if !config.cache.PVExists(pvName) {
			t.Errorf("PV doesn't exists in cache", pvName)
		}
	}
}
