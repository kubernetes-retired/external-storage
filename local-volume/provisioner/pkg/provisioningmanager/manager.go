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
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/provisioningmanager/backend"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	ref "k8s.io/client-go/tools/reference"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
)

const defaultRetryCount = 3
const defaultRetryWaitDuration = 2 * time.Second

type DynamicProvisioningManager struct {
	*common.RuntimeConfig
	storageBackends map[string]backend.StorageBackend
	labels          map[string]string
	nodeAffinityAnn string
	nodeAffinity    *v1.VolumeNodeAffinity
}

// NewManager create a DynamicProvisioningManager instance that will accept volume claims
// that are expected to be provisioned, and create PVs and backend volumes accordingly
func NewManager(config *common.RuntimeConfig) (*DynamicProvisioningManager, error) {
	// Initialize the backends
	backends := make(map[string]backend.StorageBackend)
	for class, source := range config.ProvisionSourceMap {
		if source.Lvm != nil {
			backends[class] = backend.NewLvmBackend(source.Lvm.VolumeGroup, source.MountConfig.HostDir)
		}
		if source.Fake != nil {
			backends[class] = backend.NewFakeBackend(source.Fake.Capacity, source.Fake.RootPath)
		}
		// TODO: initialize backends from other sources
	}

	// Generate labels that will be used on the provisioned PVs
	labelMap := make(map[string]string)
	for _, labelName := range config.NodeLabelsForPV {
		labelVal, ok := config.Node.Labels[labelName]
		if ok {
			labelMap[labelName] = labelVal
		}
	}

	manager := &DynamicProvisioningManager{
		RuntimeConfig:   config,
		storageBackends: backends,
		labels:          labelMap,
	}

	// Generate node affinity information,
	if config.UseAlphaAPI {
		nodeAffinity, err := common.GenerateNodeAffinity(config.Node)
		if err != nil {
			return nil, fmt.Errorf("Failed to generate node affinity: %v", err)
		}
		tmpAnnotations := map[string]string{}
		err = helper.StorageNodeAffinityToAlphaAnnotation(tmpAnnotations, nodeAffinity)
		if err != nil {
			return nil, fmt.Errorf("Failed to convert node affinity to alpha annotation: %v", err)
		}
		manager.nodeAffinityAnn = tmpAnnotations[v1.AlphaStorageNodeAffinityAnnotation]
	} else {
		volumeNodeAffinity, err := common.GenerateVolumeNodeAffinity(config.Node)
		if err != nil {
			return nil, fmt.Errorf("Failed to generate volume node affinity: %v", err)
		}
		manager.nodeAffinity = volumeNodeAffinity
	}

	return manager, nil
}

// Start starts provisioning service
func (m *DynamicProvisioningManager) Start() {
	go wait.Until(m.volumeProvisionWorker, time.Second, wait.NeverStop)
}

func (m *DynamicProvisioningManager) volumeProvisionWorker() {
	workFunc := func() bool {
		keyObj, quit := m.ProvisionQueue.Get()
		if quit {
			return true
		}
		defer m.ProvisionQueue.Done(keyObj)

		claim, ok := keyObj.(*v1.PersistentVolumeClaim)
		if !ok {
			glog.Errorf("Object is not a *v1.PersistentVolumeClaim")
			return false
		}

		pvName := getProvisionedVolumeNameForClaim(claim)
		pv, exists := m.Cache.GetPV(pvName)
		if exists {
			if pv.Spec.ClaimRef == nil ||
				pv.Spec.ClaimRef.Name != claim.Name ||
				pv.Spec.ClaimRef.Namespace != claim.Namespace {
				glog.Errorf("PV %q already exist, but not bound to claim %q", pvName, getClaimName(claim))
			}
			return false
		}

		if err := m.CreateLocalVolume(claim); err != nil {
			glog.Errorf("Error creating volumes for claim %q: %v", getClaimName(claim), err)
			// Signal back to the scheduler to retry dynamic provisioning
			// by removing the "annSelectedNode" annotation
			annotations := claim.Annotations
			delete(annotations, common.AnnSelectedNode)
			delete(annotations, common.AnnProvisionedTopology)
			claim.Annotations = annotations
			for i := 0; i <= defaultRetryCount; i++ {
				if _, err := m.APIUtil.UpdatePVC(claim); err == nil {
					break
				}
				glog.Errorf("Failed to update claim %q: %v", getClaimName(claim), err)
				time.Sleep(defaultRetryWaitDuration)
			}

			return false
		}
		return false
	}
	for {
		if quit := workFunc(); quit {
			glog.Infof("Provision worker queue shutting down")
			return
		}
	}
}

// CreateLocalVolume create volume basing on given claim,
// along with a PV object that is pre-bound to the claim
func (m *DynamicProvisioningManager) CreateLocalVolume(claim *v1.PersistentVolumeClaim) error {
	// If annProvisionerTopology was not set, then it means
	// the scheduler made a decision not taking into account
	// capacity.
	// Do not try provisioning if the capacity in StorageClass
	// has not been initialized.
	// TODO: Double check the value of the annotation if needed
	if _, ok := claim.Annotations[common.AnnProvisionedTopology]; !ok {
		return fmt.Errorf("capacity not reported yet, skip creating volumes for claim %q", getClaimName(claim))
	}

	className := helper.GetPersistentVolumeClaimClass(claim)
	storageBackend, ok := m.storageBackends[className]
	if !ok {
		// Backend does not exist, return error
		// This should not happen
		return fmt.Errorf("cannot handle volume creation of storage class %s", className)
	}

	class, err := m.APIUtil.GetStorageClass(className)
	if err != nil {
		glog.Errorf("Failed to get storage class %q: %v", className, err)
		return err
	}

	capacity := claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	sizeBytes := capacity.Value()
	// Convert to GiB with rounding up
	sizeGB := int(util.RoundUpToGiB(sizeBytes))

	// Create local volume
	volName := getProvisionedVolumeNameForClaim(claim)
	volReq := &backend.LocalVolumeReq{
		VolumeName: volName,
		SizeGB:     sizeGB,
	}

	// Prepare a claimRef to the claim early (to fail before a volume is
	// provisioned)
	claimRef, err := ref.GetReference(scheme.Scheme, claim)
	if err != nil {
		glog.Errorf("Unexpected error getting claim reference to claim %q: %v", getClaimName(claim), err)
		return nil
	}

	volInfo, err := storageBackend.CreateLocalVolume(volReq)
	if err != nil {
		return err
	}

	localPVConfig := &common.LocalPVConfig{
		Name:           volName,
		HostPath:       volInfo.VolumePath,
		Capacity:       int64(sizeGB * (1024 * 1024 * 1024)),
		StorageClass:   className,
		ProvisionerTag: m.Tag,
		Labels:         m.labels,
		AccessModes:    claim.Spec.AccessModes,
		ReclaimPolicy:  *class.ReclaimPolicy,
		ClaimRef:       claimRef,
		AdditionalAnn: map[string]string{
			common.AnnProvisionedTopology: claim.Annotations[common.AnnProvisionedTopology],
		},
	}

	if claim.Spec.VolumeMode == nil {
		// Default to filesystem
		localPVConfig.VolumeMode = v1.PersistentVolumeFilesystem
	} else {
		localPVConfig.VolumeMode = *claim.Spec.VolumeMode
	}

	if m.UseAlphaAPI {
		localPVConfig.UseAlphaAPI = true
		localPVConfig.AffinityAnn = m.nodeAffinityAnn
	} else {
		localPVConfig.NodeAffinity = m.nodeAffinity
	}

	pvSpec := common.CreateLocalPVSpec(localPVConfig)

	for trial := 0; trial <= defaultRetryCount; trial++ {
		_, err := m.APIUtil.CreatePV(pvSpec)
		if err == nil {
			break
		}
		// Recycle the volume created above if failed to create PV
		if trial >= defaultRetryCount {
			if delErr := storageBackend.DeleteLocalVolume(volName); delErr != nil {
				glog.Errorf("Error clean up volume %q: %v", volName, delErr)
			}
			return err
		}
		// Create failed, try again after a while.
		glog.Errorf("Error creating PV %q for claim %q: %v", volName, getClaimName(claim), err)
		time.Sleep(defaultRetryWaitDuration)
	}

	glog.Infof("Created PV %q for claim %q", volName, getClaimName(claim))
	return nil

}

// DeleteLocalVolume clean up the backend volume of the pv
func (m *DynamicProvisioningManager) DeleteLocalVolume(pv *v1.PersistentVolume) error {
	pvClass := helper.GetPersistentVolumeClass(pv)
	storageBackend, ok := m.storageBackends[pvClass]
	if !ok {
		// Backend does not exist, return error
		// This should not happen
		return fmt.Errorf("cannot handle volume deletion of storage class: %s", pvClass)
	}
	return storageBackend.DeleteLocalVolume(pv.Name)
}

// GetCapacity get capacity of given storage class
func (m *DynamicProvisioningManager) GetCapacity(storageClass string) int64 {
	storageBackend, exist := m.storageBackends[storageClass]
	if !exist {
		glog.Errorf("Cannot report capacity of class %q, no storage backend found", storageClass)
		return 0
	}
	capacity, err := storageBackend.GetCapacity()
	if err != nil {
		glog.Errorf("Error getting capacity of class %q : %v", storageClass, err)
		return 0
	}

	return capacity
}

func getProvisionedVolumeNameForClaim(claim *v1.PersistentVolumeClaim) string {
	return "pvc-" + string(claim.UID)
}

func getClaimName(claim *v1.PersistentVolumeClaim) string {
	return claim.Namespace + "/" + claim.Name
}
