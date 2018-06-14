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
	"reflect"
	"testing"

	"github.com/golang/glog"
	esUtil "github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/util/mount"
)

const (
	testHostDir         = "/mnt/disks"
	testMountDir        = "/discoveryPath"
	testNodeName        = "test-node"
	testProvisionerName = "test-provisioner"
)

var nodeLabels = map[string]string{
	"failure-domain.beta.kubernetes.io/zone":   "west-1",
	"failure-domain.beta.kubernetes.io/region": "west",
	common.NodeLabelKey:                        testNodeName,
	"label-that-pv-does-not-inherit":           "foo"}

var nodeLabelsForPV = []string{
	"failure-domain.beta.kubernetes.io/zone",
	"failure-domain.beta.kubernetes.io/region",
	common.NodeLabelKey,
	"non-existent-label-that-pv-will-not-get"}

var expectedPVLabels = map[string]string{
	"failure-domain.beta.kubernetes.io/zone":   "west-1",
	"failure-domain.beta.kubernetes.io/region": "west",
	common.NodeLabelKey:                        testNodeName}

var testNode = &v1.Node{
	ObjectMeta: metav1.ObjectMeta{
		Name:   testNodeName,
		Labels: nodeLabels,
	},
}

var reclaimPolicyDelete = v1.PersistentVolumeReclaimDelete

var testStorageClasses = []*storagev1.StorageClass{
	{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sc1",
		},
		ReclaimPolicy: &reclaimPolicyDelete,
	},
	{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sc2",
		},
		ReclaimPolicy: &reclaimPolicyDelete,
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
	volUtil        *util.FakeVolumeUtil
	apiUtil        *util.FakeAPIUtil
	cache          *cache.VolumeCache
	cleanupTracker *deleter.CleanupStatusTracker
}

func TestDiscoverVolumes_Basic(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
			{Name: "symlink2", Hash: 0x23645a36, VolumeType: util.FakeEntryBlock, Capacity: 100 * 1024 * 1024},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x226458a3, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test, false)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_BasicTwice(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x23645a36, VolumeType: util.FakeEntryBlock},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x226458a3, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test, false)

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
	d := testSetup(t, test, false)

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
	d := testSetup(t, test, false)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NewVolumesLater(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x23645a36, VolumeType: util.FakeEntryBlock},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x226458a3, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test, false)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)

	// Some new mount points show up
	newVols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount3", Hash: 0xf34b8003, VolumeType: util.FakeEntryFile},
			{Name: "symlink3", Hash: 0x4d24d329, VolumeType: util.FakeEntryBlock},
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
	d := testSetup(t, test, false)

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
	d := testSetup(t, test, false)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func TestDiscoverVolumes_CleaningInProgress(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
			{Name: "symlink2", Hash: 0x23645a36, VolumeType: util.FakeEntryBlock, Capacity: 100 * 1024 * 1024},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x226458a3, VolumeType: util.FakeEntryBlock},
		},
	}

	// Don't expect dir1/mount2 to be created
	expectedVols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
		},
		"dir2": {
			{Name: "mount1", Hash: 0xa7aafa3c, VolumeType: util.FakeEntryFile},
			{Name: "symlink2", Hash: 0x226458a3, VolumeType: util.FakeEntryBlock},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: expectedVols,
	}
	d := testSetup(t, test, false)

	// Mark dir1/mount2 PV as being cleaned. This one should not get created
	pvName := getPVName(vols["dir1"][1])
	test.cleanupTracker.ProcTable.MarkRunning(pvName)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func testSetup(t *testing.T, test *testConfig, useAlphaAPI bool) *Discoverer {
	test.cache = cache.NewVolumeCache()
	test.volUtil = util.NewFakeVolumeUtil(false /*deleteShouldFail*/, map[string][]*util.FakeDirEntry{})
	test.volUtil.AddNewDirEntries(testMountDir, test.dirLayout)
	test.apiUtil = util.NewFakeAPIUtil(test.apiShouldFail, test.cache)
	test.cleanupTracker = &deleter.CleanupStatusTracker{ProcTable: deleter.NewProcTable(),
		JobController: deleter.NewFakeJobController()}

	fm := &mount.FakeMounter{
		MountPoints: []mount.MountPoint{
			{Path: "/discoveryPath/dir1/mount1"},
			{Path: "/discoveryPath/dir1/mount2"},
			{Path: "/discoveryPath/dir2/mount1"},
			{Path: "/discoveryPath/dir2/mount2"},
			{Path: "/discoveryPath/dir1"},
			{Path: "/discoveryPath/dir2"},
			{Path: "/discoveryPath/dir1/mount3"},
			{Path: "/discoveryPath/dir1/mount4"},
		},
	}

	userConfig := &common.UserConfig{
		Node:            testNode,
		DiscoveryMap:    scMapping,
		NodeLabelsForPV: nodeLabelsForPV,
		UseAlphaAPI:     useAlphaAPI,
	}
	objects := make([]runtime.Object, 0)
	for _, o := range testStorageClasses {
		objects = append(objects, runtime.Object(o))
	}
	client := fake.NewSimpleClientset(objects...)
	runConfig := &common.RuntimeConfig{
		UserConfig:      userConfig,
		Cache:           test.cache,
		VolUtil:         test.volUtil,
		APIUtil:         test.apiUtil,
		Name:            testProvisionerName,
		Mounter:         fm,
		Client:          client,
		InformerFactory: informers.NewSharedInformerFactory(client, 0),
	}
	d, err := NewDiscoverer(runConfig, test.cleanupTracker)
	if err != nil {
		t.Fatalf("Error setting up test discoverer: %v", err)
	}
	// Start informers after all event listeners are registered.
	runConfig.InformerFactory.Start(wait.NeverStop)
	// Wait for all started informers' cache were synced.
	for v, synced := range runConfig.InformerFactory.WaitForCacheSync(wait.NeverStop) {
		if !synced {
			glog.Fatalf("Error syncing informer for %v", v)
		}
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
	var err error
	var volumeNodeAffinity *v1.VolumeNodeAffinity
	var nodeAffinity *v1.NodeAffinity
	var selector *v1.NodeSelector

	volumeNodeAffinity = pv.Spec.NodeAffinity
	if volumeNodeAffinity == nil {
		nodeAffinity, err = helper.GetStorageNodeAffinityFromAnnotation(pv.Annotations)
		if err != nil {
			t.Errorf("Could not get node affinity from annotation: %v", err)
			return
		}
		if nodeAffinity == nil {
			t.Errorf("No node affinity found")
			return
		}
		selector = nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	} else {
		selector = volumeNodeAffinity.Required
	}
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

func verifyPVLabels(t *testing.T, pv *v1.PersistentVolume) {
	if len(pv.Labels) == 0 {
		t.Errorf("Labels not set")
		return
	}
	eq := reflect.DeepEqual(pv.Labels, expectedPVLabels)
	if !eq {
		t.Errorf("Labels not as expected %v != %v", pv.Labels, expectedPVLabels)
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
	if roundDownCapacityPretty(capacityInt) != expectedPV.capacity {
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

func getPVName(entry *util.FakeDirEntry) string {
	return fmt.Sprintf("local-pv-%x", entry.Hash)
}

func verifyCreatedPVs(t *testing.T, test *testConfig) {
	expectedPVs := map[string]*testPVInfo{}
	for dir, files := range test.expectedVolumes {
		for _, file := range files {
			pvName := getPVName(file)
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
			t.Errorf("Did not find expected PVs %v", pvName)
			continue
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
		verifyPVLabels(t, createdPV)
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

func TestRoundDownCapacityPretty(t *testing.T) {
	var capTests = []struct {
		n        int64 // input
		expected int64 // expected result
	}{
		{100 * esUtil.KiB, 100 * esUtil.KiB},
		{10 * esUtil.MiB, 10 * esUtil.MiB},
		{100 * esUtil.MiB, 100 * esUtil.MiB},
		{10 * esUtil.GiB, 10 * esUtil.GiB},
		{10 * esUtil.TiB, 10 * esUtil.TiB},
		{9*esUtil.GiB + 999*esUtil.MiB, 9*esUtil.GiB + 999*esUtil.MiB},
		{10*esUtil.GiB + 5, 10 * esUtil.GiB},
		{10*esUtil.MiB + 5, 10 * esUtil.MiB},
		{10000*esUtil.MiB - 1, 9999 * esUtil.MiB},
		{13*esUtil.GiB - 1, 12 * esUtil.GiB},
		{63*esUtil.MiB - 10, 62 * esUtil.MiB},
		{12345, 12345},
		{10000*esUtil.GiB - 1, 9999 * esUtil.GiB},
		{3*esUtil.TiB + 2*esUtil.GiB + 1*esUtil.MiB, 3*esUtil.TiB + 2*esUtil.GiB},
	}
	for _, tt := range capTests {
		actual := roundDownCapacityPretty(tt.n)
		if actual != tt.expected {
			t.Errorf("roundDownCapacityPretty(%d): expected %d, actual %d", tt.n, tt.expected, actual)
		}
	}
}

func TestDiscoverVolumes_NotMountPoint(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
			// mount5 is not listed in the FakeMounter MountPoints setup for testing
			{Name: "mount5", Hash: 0x79412c38, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024 * 1024},
		},
	}
	expectedVols := map[string][]*util.FakeDirEntry{
		"dir1": {
			{Name: "mount1", Hash: 0xaaaafef5, VolumeType: util.FakeEntryFile, Capacity: 100 * 1024},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: expectedVols,
	}
	d := testSetup(t, test, false)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestUseAlphaAPI(t *testing.T) {
	vols := map[string][]*util.FakeDirEntry{}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test, false)
	if d.UseAlphaAPI {
		t.Fatal("UseAlphaAPI should be false")
	}
	if len(d.nodeAffinityAnn) != 0 || d.nodeAffinity == nil {
		t.Fatal("the value nodeAffinityAnn shouldn't be set while nodeAffinity should")
	}

	d = testSetup(t, test, true)
	if !d.UseAlphaAPI {
		t.Fatal("UseAlphaAPI should be true")
	}
	if d.nodeAffinity != nil || len(d.nodeAffinityAnn) == 0 {
		t.Fatal("the value nodeAffinityAnn should be set while nodeAffinity should not")
	}
}
