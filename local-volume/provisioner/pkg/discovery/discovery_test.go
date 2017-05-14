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
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/types"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"
)

const (
	testHostDir  = "/mnt/disks"
	testMountDir = "/discoveryPath"
	testNode     = "test-node"
)

type testConfig struct {
	// Directory is relative path to hostdir
	dirFiles map[string][]string
	// Directory is relative path to basedir
	scMapping map[string]string
	// The directory-file mapping expected to be created as PVs
	expectedVolumes map[string][]string
	// True if testing api failure
	apiShouldFail bool
	// The rest are set during setup
	volUtil *util.FakeVolumeUtil
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

func TestDiscoverVolumes_Basic(t *testing.T) {
	vols := map[string][]string{
		"dir1": []string{"mount1", "mount2"},
		"dir2": []string{"mount1", "mount2"},
	}
	test := &testConfig{
		dirFiles:        vols,
		expectedVolumes: vols,
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_BasicTwice(t *testing.T) {
	vols := map[string][]string{
		"dir1": []string{"mount1", "mount2"},
		"dir2": []string{"mount1", "mount2"},
	}
	test := &testConfig{
		dirFiles:        vols,
		expectedVolumes: vols,
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)

	// Second time should not create any new volumes
	test.expectedVolumes = map[string][]string{}
	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NoDir(t *testing.T) {
	vols := map[string][]string{}
	test := &testConfig{
		dirFiles:        vols,
		expectedVolumes: vols,
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_EmptyDir(t *testing.T) {
	vols := map[string][]string{
		"dir1": []string{},
	}
	test := &testConfig{
		dirFiles:        vols,
		expectedVolumes: vols,
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()
	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_NewVolumesLater(t *testing.T) {
	vols := map[string][]string{
		"dir1": []string{"mount1", "mount2"},
		"dir2": []string{"mount1", "mount2"},
	}
	test := &testConfig{
		dirFiles:        vols,
		expectedVolumes: vols,
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)

	// Some new mount points show up
	newVols := map[string][]string{
		"dir1": []string{"mount3", "mount4"},
	}
	test.volUtil.AddNewFiles(testMountDir, newVols)
	test.expectedVolumes = newVols

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
}

func TestDiscoverVolumes_CreatePVFails(t *testing.T) {
	vols := map[string][]string{
		"dir1": []string{"mount1", "mount2"},
		"dir2": []string{"mount1", "mount2"},
	}
	test := &testConfig{
		apiShouldFail:   true,
		dirFiles:        vols,
		expectedVolumes: map[string][]string{},
		scMapping: map[string]string{
			"sc1": "dir1",
			"sc2": "dir2",
		},
	}
	d := testSetup(test)

	d.DiscoverLocalVolumes()

	verifyCreatedPVs(t, test)
	verifyPVsNotInCache(t, test)
}

func testSetup(t *testConfig) *Discoverer {
	t.volUtil = util.NewFakeVolumeUtil()
	t.volUtil.AddNewFiles(testMountDir, t.dirFiles)
	t.apiUtil = util.NewFakeAPIUtil(t.apiShouldFail)
	t.cache = cache.NewVolumeCache()

	userConfig := &types.UserConfig{
		NodeName:     testNode,
		MountDir:     testMountDir,
		HostDir:      testHostDir,
		DiscoveryMap: t.scMapping,
	}
	config := &types.RuntimeConfig{
		UserConfig: userConfig,
		Cache:      t.cache,
		VolUtil:    t.volUtil,
		APIUtil:    t.apiUtil,
	}
	return NewDiscoverer(config)
}

func findSCName(t *testing.T, targetDir string, config *testConfig) string {
	for sc, dir := range config.scMapping {
		if dir == targetDir {
			return sc
		}
	}
	t.Fatalf("Failed to find SC name for directory %v", targetDir)
	return ""
}

func verifyCreatedPVs(t *testing.T, config *testConfig) {
	expectedPVs := map[string]string{}
	for dir, files := range config.expectedVolumes {
		sc := findSCName(t, dir, config)
		for _, file := range files {
			pvName := fmt.Sprintf("%v-%v-%v", sc, testNode, file)
			path := filepath.Join(testHostDir, dir, file)
			expectedPVs[pvName] = path
		}
	}

	createdPVs := config.apiUtil.GetAndResetCreatedPVs()
	expectedLen := len(expectedPVs)
	actualLen := len(createdPVs)
	if expectedLen != actualLen {
		t.Errorf("Expected %v created PVs, got %v", expectedLen, actualLen)
	}

	for pvName := range createdPVs {
		expectedPath, found := expectedPVs[pvName]
		if !found {
			t.Errorf("Did not expect created PVs %v", pvName)
		}
		// TODO: replace when API is checked in
		// if pv.PersistentVolumeSource.Local.Path != expectedPath {
		if false {
			// TODO: fix when api
			t.Errorf("Expected path %q, got %q", expectedPath, expectedPath)
		}
		if !config.cache.PVExists(pvName) {
			t.Errorf("PV %q not in cache", pvName)
		}
	}
}

func verifyPVsNotInCache(t *testing.T, config *testConfig) {
	for dir, files := range config.dirFiles {
		sc := findSCName(t, dir, config)
		for _, file := range files {
			pvName := fmt.Sprintf("%v-%v-%v", sc, testNode, file)
			if config.cache.PVExists(pvName) {
				t.Errorf("Expected PV %q to not be in cache", pvName)
			}
		}
	}
}
