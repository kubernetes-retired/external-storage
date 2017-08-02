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

package discovery

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/api/v1/helper"
)

const (
	testHostDir         = "/mnt/disks"
	testMountDir        = "/discoveryPath"
	testNodeName        = "test-node"
	testProvisionerName = "test-provisioner"
)

var testNode = &v1.Node{
	ObjectMeta: metav1.ObjectMeta{
		Name: testNodeName,
		Labels: map[string]string{
			common.NodeLabelKey: testNodeName,
		},
	},
}

var scMapping = map[string]common.MountConfig{
	"sc1": {
		HostDir:  testHostDir + "/dir1",
		MountDir: testMountDir + "/dir1",
	},
	"sc2": {
		HostDir:  testHostDir + "/dir2",
		MountDir: testMountDir + "/dir2",
	},
}

type testConfig struct {
	// The directory layout for the test
	// Key = directory, Value = list of volumes under that directory
	dirLayout map[string][]*util.FakeDirEntry
	// The volumes that are expected to be created as PVs
	// Key = directory, Value = list of volumes under that directory
	expectedVolumes map[string][]*util.FakeDirEntry
	// True if testing api failure
	apiShouldFail bool
	// The rest are set during setup
	volUtil *util.FakeVolumeUtil
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

func TestDiscoverVolumes_Basic(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
			{Name: "mount2", Hash: 0x79412c38, VolumeType: util.FakeEntryBlock, Capacity: 100 * 1024 * 1024},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x7c4130f1, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_BasicTwice(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x79412c38, VolumeType: util.FakeEntryBlock},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x7c4130f1, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)

	// Second time should not create any new volumes
	test.expectedVolumes = map[string][]*util.FakeDirEntry{}
	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NoDir(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_EmptyDir(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NewVolumesLater(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x79412c38, VolumeType: util.FakeEntryBlock},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x7c4130f1, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)

	// Some new mount points show up
	newVols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount3", Hash: 0xf34b8003, VolumeType: util.FakeEntryFile},
			{Name: "mount4", Hash: 0x144e29de, VolumeType: util.FakeEntryBlock},
		},
	}
	test.volUtil.AddNewDirEntries(testMountDir, newVols)
	test.expectedVolumes = newVols

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_CreatePVFails(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x79412c38, VolumeType: util.FakeEntryFile},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "mount2", Hash: 0x7c4130f1, VolumeType: util.FakeEntryFile},
		},
	}
	test := &testConfig{
		apiShouldFail:   true,
		dirLayout:       vols,
		expectedVolumes: map[string][]*util.FakeDirEntry{},
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func TestDiscoverVolumes_BadVolume(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", VolumeType: util.FakeEntryUnknown},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: map[string][]*util.FakeDirEntry{},
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func testSetup(t *testing.T, test *testConfig) *Discoverer {
	test.cache = cache.NewVolumeCache()
	test.volUtil = util.NewFakeVolumeUtil(false)
	test.volUtil.AddNewDirEntries(testMountDir, test.dirLayout)
	test.apiUtil = util.NewFakeAPIUtil(test.apiShouldFail, test.cache)

	userConfig := &common.UserConfig{
		Node:         testNode,
		DiscoveryMap: scMapping,
	}
	runConfig := &common.RuntimeConfig{
		UserConfig: userConfig,
		Cache:      test.cache,
		VolUtil:    test.volUtil,
		APIUtil:    test.apiUtil,
		Name:       testProvisionerName,
	}
	d, err := NewDiscoverer(runConfig)
	if err != nil {
		t.Fatalf("Error setting up test discoverer: %v", err)
	}
	return d
}

func findSCName(t *testing.T, targetDir string, test *testConfig) string {
	for sc, config := range scMapping {
		_, dir := filepath.Split(config.HostDir)
		if dir == targetDir {
			return sc
		}
	}
	t.Fatalf("Failed to find SC Name for directory %v", targetDir)
	return ""
}

func verifyNodeAffinity(t *testing.T, pv *v1.PersistentVolume) {
	affinity, err := helper.GetStorageNodeAffinityFromAnnotation(pv.Annotations)
	if err != nil {
		t.Errorf("Could not get node affinity from annotation: %v", err)
		return
	}
	if affinity == nil {
		t.Errorf("No node affinity found")
		return
	}

	selector := affinity.RequiredDuringSchedulingIgnoredDuringExecution
	if selector == nil {
		t.Errorf("NodeAffinity node selector is nil")
		return
	}
	terms := selector.NodeSelectorTerms
	if len(terms) != 1 {
		t.Errorf("Node selector term count is %v, expected 1", len(terms))
		return
	}
	reqs := terms[0].MatchExpressions
	if len(reqs) != 1 {
		t.Errorf("Node selector term requirements count is %v, expected 1", len(reqs))
		return
	}

	req := reqs[0]
	if req.Key != common.NodeLabelKey {
		t.Errorf("Node selector requirement key is %v, expected %v", req.Key, common.NodeLabelKey)
	}
	if req.Operator != v1.NodeSelectorOpIn {
		t.Errorf("Node selector requirement operator is %v, expected %v", req.Operator, v1.NodeSelectorOpIn)
	}
	if len(req.Values) != 1 {
		t.Errorf("Node selector requirement value count is %v, expected 1", len(req.Values))
		return
	}
	if req.Values[0] != testNodeName {
		t.Errorf("Node selector requirement value is %v, expected %v", req.Values[0], testNodeName)
	}
}

func verifyProvisionerName(t *testing.T, pv *v1.PersistentVolume) {
	if len(pv.Annotations) == 0 {
		t.Errorf("Annotations not set")
		return
	}
	name, found := pv.Annotations[common.AnnProvisionedBy]
	if !found {
		t.Errorf("Provisioned by annotations not set")
		return
	}
	if name != testProvisionerName {
		t.Errorf("Provisioned name is %q, expected %q", name, testProvisionerName)
	}
}

func verifyCapacity(t *testing.T, createdPV *v1.PersistentVolume, expectedPV *testPVInfo) {
	capacity, ok := createdPV.Spec.Capacity[v1.ResourceStorage]
	if !ok {
		t.Errorf("Unexpected empty resource storage")
	}
	capacityInt, ok := capacity.AsInt64()
	if !ok {
		t.Errorf("Unable to convert resource storage into int64")
	}
	if capacityInt != expectedPV.capacity {
		t.Errorf("Expected capacity %d, got %d", expectedPV.capacity, capacityInt)
	}
}

// testPVInfo contains all the fields we are intested in validating.
type testPVInfo struct {
	pvName       string
	path         string
	capacity     int64
	storageClass string
}

func verifyCreatedPVs(t *testing.T, test *testConfig) {
	expectedPVs := map[string]*testPVInfo{}
	for dir, files := range test.expectedVolumes {
		for _, file := range files {
			pvName := fmt.Sprintf("local-pv-%x", file.Hash)
			path := filepath.Join(testHostDir, dir, file.Name)
			expectedPVs[pvName] = &testPVInfo{
				pvName:       pvName,
				path:         path,
				capacity:     file.Capacity,
				storageClass: findSCName(t, dir, test),
			}
		}
	}

	createdPVs := test.apiUtil.GetAndResetCreatedPVs()
	expectedLen := len(expectedPVs)
	actualLen := len(createdPVs)
	if expectedLen != actualLen {
		t.Errorf("Expected %v created PVs, got %v", expectedLen, actualLen)
	}

	for pvName, createdPV := range createdPVs {
		expectedPV, found := expectedPVs[pvName]
		if !found {
			t.Errorf("Did not expect created PVs %v", pvName)
		}
		if createdPV.Spec.PersistentVolumeSource.Local.Path != expectedPV.path {
			t.Errorf("Expected path %q, got %q", expectedPV.path, createdPV.Spec.PersistentVolumeSource.Local.Path)
		}
		if createdPV.Spec.StorageClassName != expectedPV.storageClass {
			t.Errorf("Expected storage class %q, got %q", expectedPV.storageClass, createdPV.Spec.StorageClassName)
		}
		_, exists := test.cache.GetPV(pvName)
		if !exists {
			t.Errorf("PV %q not in cache", pvName)
		}

		verifyProvisionerName(t, createdPV)
		verifyNodeAffinity(t, createdPV)
		verifyCapacity(t, createdPV, expectedPV)
		// TODO: Verify volume type once that is supported in the API.
	}
}

func verifyPVsNotInCache(t *testing.T, test *testConfig) {
	for _, files := range test.dirLayout {
		for _, file := range files {
			pvName := fmt.Sprintf("local-pv-%x", file.Hash)
			_, exists := test.cache.GetPV(pvName)
			if exists {
				t.Errorf("Expected PV %q to not be in cache", pvName)
			}
		}
	}
}
