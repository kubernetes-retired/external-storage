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

package provisioningmanager

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
)

const (
	testNamespace      = "testns"
	testNodeName       = "test-node"
	testClass          = "sc1"
	testProvisionerTag = "test-provisioner"
	testPath           = "/dir1"
)

var testCapacity = resource.MustParse("2Gi")

type testConfig struct {
	// Pre-installed storage classes
	classes map[string]*storagev1.StorageClass
	// True if testing api failure
	apiShouldFail bool
	// The rest are set during setup
	apiUtil *util.FakeAPIUtil
	cache   *cache.VolumeCache
}

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

var scMapping = map[string]common.ProvisionSourceConfig{
	"sc1": {
		Fake: &common.FakeSource{
			RootPath: testPath,
		},
	},
}

func makeClass(name, topologyKey string, capacity resource.Quantity) *storagev1.StorageClass {
	reclaimPolicy := v1.PersistentVolumeReclaimDelete
	class := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		ReclaimPolicy: &reclaimPolicy,
	}
	if topologyKey != "" {
		class.Status = &storagev1.StorageClassStatus{
			Capacity: &storagev1.StorageClassCapacity{
				TopologyKeys: []string{topologyKey},
				Capacities:   map[string]resource.Quantity{testNodeName: capacity},
			},
		}
	}
	return class
}

func makeClaim(name, namespace, className string, capacity resource.Quantity, hasTopologyAnn bool) *v1.PersistentVolumeClaim {
	fs := v1.PersistentVolumeFilesystem
	claim := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			UID:         types.UID(namespace + name),
			Annotations: map[string]string{},
			SelfLink:    fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", namespace, name),
		},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &className,
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): capacity,
				},
			},
			VolumeMode: &fs,
		},
	}

	if hasTopologyAnn {
		claim.Annotations[common.AnnProvisionedTopology] = common.NodeLabelKey + ":" + testNodeName
	}

	return claim
}

func testSetup(t *testing.T, test *testConfig, useAlphaAPI bool) *DynamicProvisioningManager {
	test.cache = cache.NewVolumeCache()
	test.apiUtil = util.NewFakeAPIUtil(test.apiShouldFail, test.cache, test.classes)

	userConfig := &common.UserConfig{
		Node:               testNode,
		ProvisionSourceMap: scMapping,
		NodeLabelsForPV:    nodeLabelsForPV,
		UseAlphaAPI:        useAlphaAPI,
	}
	runConfig := &common.RuntimeConfig{
		UserConfig: userConfig,
		Cache:      test.cache,
		APIUtil:    test.apiUtil,
		Tag:        testProvisionerTag,
	}
	s, err := NewManager(runConfig)
	if err != nil {
		t.Fatalf("Error setting up test provisioning manager: %v", err)
	}
	return s
}

func TestCreateLocalVolume(t *testing.T) {
	testCases := []struct {
		description    string
		claimName      string
		claimNs        string
		className      string
		hasTopologyAnn bool
		apiShouldFail  bool
		expErr         error
	}{
		{
			description:    "Successful provision",
			claimName:      "claim1",
			claimNs:        testNamespace,
			className:      testClass,
			hasTopologyAnn: true,
			apiShouldFail:  false,
			expErr:         nil,
		},
		{
			description:    "Provision fails: Unknown class",
			claimName:      "claim1",
			claimNs:        testNamespace,
			className:      "sc2",
			hasTopologyAnn: true,
			apiShouldFail:  false,
			expErr:         fmt.Errorf("cannot handle volume creation of storage class sc2"),
		},
		{
			description:    "Provision fails: has no provisioned topology annotation",
			claimName:      "claim1",
			claimNs:        testNamespace,
			className:      testClass,
			hasTopologyAnn: false,
			apiShouldFail:  false,
			expErr:         fmt.Errorf("capacity not reported yet, skip creating volumes for claim \"testns/claim1\""),
		},
		{
			description:    "Provision fails: failed to create PV object",
			claimName:      "claim1",
			claimNs:        testNamespace,
			className:      testClass,
			hasTopologyAnn: true,
			apiShouldFail:  true,
			expErr:         fmt.Errorf("API failed"),
		},
	}
	for _, testCase := range testCases {
		testConf := &testConfig{
			classes: map[string]*storagev1.StorageClass{
				testClass: makeClass("sc1", common.NodeLabelKey, testCapacity),
			},
			apiShouldFail: testCase.apiShouldFail,
		}
		s := testSetup(t, testConf, false)
		claim := makeClaim(testCase.claimName, testCase.claimNs, testCase.className, testCapacity, testCase.hasTopologyAnn)
		err := s.CreateLocalVolume(claim)
		if !reflect.DeepEqual(err, testCase.expErr) {
			t.Errorf("Provision error (%v). expected error: %v but got: %v",
				testCase.description, testCase.expErr, err)
		}
		pvName := getProvisionedVolumeNameForClaim(claim)
		if err == nil {
			createdPVs := testConf.apiUtil.GetAndResetCreatedPVs()
			if len(createdPVs) != 1 {
				t.Errorf("Expected 1 created PVs, got %d", len(createdPVs))
				break
			}

			pv, exist := createdPVs[pvName]
			if !exist {
				t.Errorf("PV %q not found in created PVs", pvName)
			}
			_, exists := testConf.cache.GetPV(pvName)
			if !exists {
				t.Errorf("PV %q not in cache", pvName)
			}
			verifyCreatedPV(t, pv, claim)
		} else {
			if _, exists := testConf.cache.GetPV(pvName); exists {
				t.Errorf("Expected PV %q to not be in cache", pvName)
			}
		}
	}

}

func verifyCreatedPV(t *testing.T, pv *v1.PersistentVolume, claim *v1.PersistentVolumeClaim) {
	if pv.Name != getProvisionedVolumeNameForClaim(claim) {
		t.Errorf("Expected PV name %q, got %q", getProvisionedVolumeNameForClaim(claim), pv.Name)
	}
	if pv.Spec.StorageClassName != testClass {
		t.Errorf("Expected storage class %q, got %q", testClass, pv.Spec.StorageClassName)
	}
	if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Name != claim.Name || pv.Spec.ClaimRef.Namespace != claim.Namespace {
		t.Errorf("Unexpected claim reference: %v", pv.Spec.ClaimRef)
	}
	verifyProvisionerName(t, pv)
	verifyNodeAffinity(t, pv)
	verifyPVLabels(t, pv)
	verifyCapacity(t, pv)
	verifyPath(t, pv)
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
	tag, found := pv.Annotations[common.AnnProvisionedBy]
	if !found {
		t.Errorf("Provisioned by annotations not set")
		return
	}
	if tag != testProvisionerTag {
		t.Errorf("Provisioned name is %q, expected %q", tag, testProvisionerTag)
	}
}

func verifyCapacity(t *testing.T, createdPV *v1.PersistentVolume) {
	capacity, ok := createdPV.Spec.Capacity[v1.ResourceStorage]
	if !ok {
		t.Errorf("Unexpected empty resource storage")
	}
	if capacity.Cmp(testCapacity) != 0 {
		t.Errorf("Expected capacity %v, got %v", testCapacity, capacity)
	}

}

func verifyPath(t *testing.T, createdPV *v1.PersistentVolume) {
	expectedPath := testPath + "/" + createdPV.Name
	actualPath := createdPV.Spec.Local.Path
	if actualPath != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, actualPath)
	}
}
