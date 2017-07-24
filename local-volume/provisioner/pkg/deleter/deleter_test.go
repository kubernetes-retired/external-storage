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
	"path/filepath"
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	testHostDir  = "/mnt/disks"
	testMountDir = "/discoveryPath"
)

type testConfig struct {
	apiShouldFail       bool
	volDeleteShouldFail bool
	// Precreated PVs
	vols map[string]*testVol
	// Expected names of deleted PV
	expectedDeletedPVs map[string]string
	// The remaining fields are set during setup
	volUtil *util.FakeVolumeUtil
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

type testVol struct {
	pvPhase v1.PersistentVolumePhase
}

func TestDeleteVolumes_Basic(t *testing.T) {
	vols := map[string]*testVol{
		"pv1": {
			pvPhase: v1.VolumePending,
		},
		"pv2": {
			pvPhase: v1.VolumeAvailable,
		},
		"pv3": {
			pvPhase: v1.VolumeBound,
		},
		"pv4": {
			pvPhase: v1.VolumeReleased,
		},
		"pv5": {
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
		"pv4": {
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
		"pv4": {
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

func TestDeleteVolumes_CleanupFails(t *testing.T) {
	vols := map[string]*testVol{
		"pv4": {
			pvPhase: v1.VolumeReleased,
		},
	}
	test := &testConfig{
		volDeleteShouldFail: true,
		vols:                vols,
		expectedDeletedPVs:  map[string]string{},
	}
	d := testSetup(t, test)

	d.DeletePVs()
	verifyDeletedPVs(t, test)
	verifyPVExists(t, test)
}

func testSetup(t *testing.T, config *testConfig) *Deleter {
	config.cache = cache.NewVolumeCache()
	config.volUtil = util.NewFakeVolumeUtil(config.volDeleteShouldFail)
	config.apiUtil = util.NewFakeAPIUtil(false, config.cache)

	fakePath := filepath.Join(testHostDir, "test-dir")
	// Precreate PVs
	for pvName, vol := range config.vols {
		pv := common.CreateLocalPVSpec(&common.LocalPVConfig{
			Name:         pvName,
			HostPath:     fakePath,
			StorageClass: "sc1",
		})
		pv.Status.Phase = vol.pvPhase

		_, err := config.apiUtil.CreatePV(pv)
		if err != nil {
			t.Fatalf("Error creating fake PV: %v", err)
		}
		config.cache.AddPV(pv)
	}

	config.apiUtil = util.NewFakeAPIUtil(config.apiShouldFail, config.cache)
	userConfig := &common.UserConfig{
		DiscoveryMap: map[string]common.MountConfig{
			"sc1": {
				HostDir:  testHostDir + "/test-dir",
				MountDir: testMountDir + "/test-dir",
			},
		},
	}
	fakeRecorder := &record.FakeRecorder{}
	runtimeConfig := &common.RuntimeConfig{
		UserConfig: userConfig,
		Cache:      config.cache,
		VolUtil:    config.volUtil,
		APIUtil:    config.apiUtil,
		Recorder:   fakeRecorder,
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
		_, found = config.cache.GetPV(pvName)
		if found {
			t.Errorf("PV %q still exists in cache", pvName)
		}
	}
}

func verifyPVExists(t *testing.T, config *testConfig) {
	for pvName := range config.vols {
		_, found := config.cache.GetPV(pvName)
		if !found {
			t.Errorf("PV %q doesn't exist in cache", pvName)
		}
	}
}
