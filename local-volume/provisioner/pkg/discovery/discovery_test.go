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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
	v1helper "k8s.io/client-go/pkg/api/v1/helper"
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

var scMapping = map[string]string{
	"sc1": "dir1",
	"sc2": "dir2",
}

type testConfig struct {
	// The directory layout for the test
	// Key = directory, Value = list of volumes under that directory
	dirLayout map[string][]*util.FakeFile
	// The volumes that are expected to be created as PVs
	// Key = directory, Value = list of volumes under that directory
	expectedVolumes map[string][]*util.FakeFile
	// True if testing api failure
	apiShouldFail bool
	// The rest are set during setup
	volUtil *util.FakeVolumeUtil
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

func TestDiscoverVolumes_Basic(t *testing.T) {
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xaaaafef5},
			&util.FakeFile{Name: "mount2", Hash: 0x79412c38},
		},
		"dir2": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xa7aafa3c},
			&util.FakeFile{Name: "mount2", Hash: 0x7c4130f1},
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
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xaaaafef5},
			&util.FakeFile{Name: "mount2", Hash: 0x79412c38},
		},
		"dir2": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xa7aafa3c},
			&util.FakeFile{Name: "mount2", Hash: 0x7c4130f1},
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
	test.expectedVolumes = map[string][]*util.FakeFile{}
	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NoDir(t *testing.T) {
	vols := map[string][]*util.FakeFile{}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: vols,
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_EmptyDir(t *testing.T) {
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{},
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
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xaaaafef5},
			&util.FakeFile{Name: "mount2", Hash: 0x79412c38},
		},
		"dir2": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xa7aafa3c},
			&util.FakeFile{Name: "mount2", Hash: 0x7c4130f1},
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
	newVols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount3", Hash: 0xf34b8003},
			&util.FakeFile{Name: "mount4", Hash: 0x144e29de},
		},
	}
	test.volUtil.AddNewFiles(testMountDir, newVols)
	test.expectedVolumes = newVols

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_CreatePVFails(t *testing.T) {
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xaaaafef5},
			&util.FakeFile{Name: "mount2", Hash: 0x79412c38},
		},
		"dir2": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", Hash: 0xa7aafa3c},
			&util.FakeFile{Name: "mount2", Hash: 0x7c4130f1},
		},
	}
	test := &testConfig{
		apiShouldFail:   true,
		dirLayout:       vols,
		expectedVolumes: map[string][]*util.FakeFile{},
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func TestDiscoverVolumes_BadVolume(t *testing.T) {
	vols := map[string][]*util.FakeFile{
		"dir1": []*util.FakeFile{
			&util.FakeFile{Name: "mount1", IsNotDir: true},
		},
	}
	test := &testConfig{
		dirLayout:       vols,
		expectedVolumes: map[string][]*util.FakeFile{},
	}
	d := testSetup(t, test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func testSetup(t *testing.T, test *testConfig) *Discoverer {
	test.cache = cache.NewVolumeCache()
	test.volUtil = util.NewFakeVolumeUtil(false)
	test.volUtil.AddNewFiles(testMountDir, test.dirLayout)
	test.apiUtil = util.NewFakeAPIUtil(test.apiShouldFail, test.cache)

	userConfig := &common.UserConfig{
		Node:         testNode,
		MountDir:     testMountDir,
		HostDir:      testHostDir,
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
	for sc, dir := range scMapping {
		if dir == targetDir {
			return sc
		}
	}
	t.Fatalf("Failed to find SC Name for directory %v", targetDir)
	return ""
}

func verifyNodeAffinity(t *testing.T, pv *v1.PersistentVolume) {
	affinity, err := v1helper.GetStorageNodeAffinityFromAnnotation(pv.Annotations)
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

func verifyCreatedPVs(t *testing.T, test *testConfig) {
	expectedPVs := map[string]string{}
	for dir, files := range test.expectedVolumes {
		for _, file := range files {
			pvName := fmt.Sprintf("local-pv-%x", file.Hash)
			path := filepath.Join(testHostDir, dir, file.Name)
			expectedPVs[pvName] = path
		}
	}

	createdPVs := test.apiUtil.GetAndResetCreatedPVs()
	expectedLen := len(expectedPVs)
	actualLen := len(createdPVs)
	if expectedLen != actualLen {
		t.Errorf("Expected %v created PVs, got %v", expectedLen, actualLen)
	}

	for pvName, pv := range createdPVs {
		expectedPath, found := expectedPVs[pvName]
		if !found {
			t.Errorf("Did not expect created PVs %v", pvName)
		}
		if pv.Spec.PersistentVolumeSource.Local.Path != expectedPath {
			t.Errorf("Expected path %q, got %q", expectedPath, expectedPath)
		}
		_, exists := test.cache.GetPV(pvName)
		if !exists {
			t.Errorf("PV %q not in cache", pvName)
		}
		// TODO: verify storage class
		verifyProvisionerName(t, pv)
		verifyNodeAffinity(t, pv)
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
