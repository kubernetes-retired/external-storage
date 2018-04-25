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

package capacityreconciler

import (
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testNodeName = "test-node"
	testClass    = "sc1"
	capacity1Gi  = 1 * 1024 * 1024 * 1024
	capacity2Gi  = 2 * 1024 * 1024 * 1024
)

type testConfig struct {
	// Pre-installed storage classes
	classes map[string]*storagev1.StorageClass
	// Classes after update
	expectedClasses map[string]*storagev1.StorageClass
	// True if testing api failure
	apiShouldFail bool
	// The rest are set during setup
	apiUtil *util.FakeAPIUtil
}

var testNode = &v1.Node{
	ObjectMeta: metav1.ObjectMeta{
		Name: testNodeName,
		Labels: map[string]string{
			common.NodeLabelKey: testNodeName,
		},
	},
}

var scMapping = map[string]common.ProvisionSourceConfig{
	testClass: {
		Fake: &common.FakeSource{
			Capacity: capacity2Gi,
			RootPath: "dir1",
		},
	},
}

func getCapacity(className string) int64 {
	sc, exist := scMapping[className]
	if !exist {
		return 0
	}
	return sc.Fake.Capacity
}

func makeClass(name, topologyKey string, size int64) *storagev1.StorageClass {
	class := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if topologyKey != "" {
		size := *resource.NewQuantity(size, resource.BinarySI)
		class.Status = &storagev1.StorageClassStatus{
			Capacity: &storagev1.StorageClassCapacity{
				MinVolumeSize: defaultMinVolumeSize,
				TopologyKeys:  []string{topologyKey},
				Capacities:    map[string]resource.Quantity{testNodeName: size},
			},
		}
	}
	return class
}

func testSetup(t *testing.T, test *testConfig) *CapacityReconciler {
	test.apiUtil = util.NewFakeAPIUtil(test.apiShouldFail, cache.NewVolumeCache(), test.classes)

	userConfig := &common.UserConfig{
		Node:               testNode,
		ProvisionSourceMap: scMapping,
	}
	runConfig := &common.RuntimeConfig{
		UserConfig: userConfig,
		APIUtil:    test.apiUtil,
	}
	return NewReconciler(runConfig, getCapacity)
}

func TestReconcileCapacity_Changed(t *testing.T) {
	test := &testConfig{
		classes: map[string]*storagev1.StorageClass{
			testClass: makeClass(testClass, common.NodeLabelKey, capacity1Gi),
		},
		expectedClasses: map[string]*storagev1.StorageClass{
			// Should update capacity with capacity reported by the backend
			testClass: makeClass(testClass, common.NodeLabelKey, capacity2Gi),
		},
		apiShouldFail: false,
	}
	s := testSetup(t, test)
	s.reconcileWorker()

	verifyStorageClass(t, test)
}

func TestReconcileCapacity_Initialized(t *testing.T) {
	test := &testConfig{
		classes: map[string]*storagev1.StorageClass{
			testClass: makeClass(testClass, "", 0),
		},
		expectedClasses: map[string]*storagev1.StorageClass{
			// Should init capacity with capacity reported by the backend
			testClass: makeClass(testClass, common.NodeLabelKey, capacity2Gi),
		},
		apiShouldFail: false,
	}
	s := testSetup(t, test)
	s.reconcileWorker()

	verifyStorageClass(t, test)
}

func TestReconcileCapacity_InvalidTokpologyKey(t *testing.T) {
	test := &testConfig{
		classes: map[string]*storagev1.StorageClass{
			testClass: makeClass(testClass, "invalid-key", 0),
		},
		expectedClasses: map[string]*storagev1.StorageClass{
			// Should skip if key not equal to "kubernetes.io/hostname"
			testClass: makeClass(testClass, "invalid-key", 0),
		},
		apiShouldFail: false,
	}
	s := testSetup(t, test)
	s.reconcileWorker()

	verifyStorageClass(t, test)
}

func TestReconcileCapacity_UpdateClassFails(t *testing.T) {
	test := &testConfig{
		classes: map[string]*storagev1.StorageClass{
			testClass: makeClass(testClass, common.NodeLabelKey, capacity1Gi),
		},
		expectedClasses: map[string]*storagev1.StorageClass{
			// Capacity not changed if failed to update class object
			testClass: makeClass(testClass, common.NodeLabelKey, capacity1Gi),
		},
		apiShouldFail: true,
	}
	s := testSetup(t, test)
	s.reconcileWorker()

	verifyStorageClass(t, test)
}

func verifyStorageClass(t *testing.T, test *testConfig) {
	for name, expectedClass := range test.expectedClasses {
		storedClass, err := test.apiUtil.GetStorageClass(name)
		if err != nil {
			t.Errorf("Could not get storage class %s: %v", name, err)
		}
		storedCapacity, initialized := getClassCapacity(storedClass)
		expectedCapacity, expectInitialized := getClassCapacity(expectedClass)
		if initialized != expectInitialized ||
			storedCapacity.Cmp(expectedCapacity) != 0 {
			t.Errorf("Expected class %v, got %v", expectedClass, storedClass)
		}
	}
}

func getClassCapacity(class *storagev1.StorageClass) (resource.Quantity, bool) {
	if class.Status == nil || class.Status.Capacity == nil {
		return resource.MustParse("0"), false
	}
	return class.Status.Capacity.Capacities[testNodeName], true
}
