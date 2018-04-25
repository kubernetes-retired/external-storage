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
	"fmt"
	"math/rand"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	storage "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
)

const defaultRetryCount = 3
const defaultRetryWaitDuration = 2 * time.Second

// The minimum size of provisioned volumes is default to 1Gi
var defaultMinVolumeSize = resource.MustParse("1Gi")

type capacityGetFuncType func(string) int64

type CapacityReconciler struct {
	*common.RuntimeConfig
	capacityGetFunc capacityGetFuncType
}

func NewReconciler(config *common.RuntimeConfig, capacityGetFunc capacityGetFuncType) *CapacityReconciler {
	return &CapacityReconciler{
		RuntimeConfig:   config,
		capacityGetFunc: capacityGetFunc,
	}
}

func (r *CapacityReconciler) Reconcile() {
	tickerPeriod := time.Second
	// In case the provisioners on different nodes start in rapid succession,
	// Let the worker wait for a random portion of tickerPeriod before reconciling.
	time.Sleep(time.Duration(rand.Float64() * float64(tickerPeriod)))
	go wait.Until(r.reconcileWorker, 120*time.Second, wait.NeverStop)
}

func (r *CapacityReconciler) reconcileWorker() {
	nodeHostName, found := r.Node.Labels[common.NodeLabelKey]
	if !found {
		glog.Errorf("Node does not have expected label %s", common.NodeLabelKey)
		return
	}
	for className := range r.ProvisionSourceMap {
		// Return true if need to retry
		workFunc := func() bool {
			// Size in GiB
			actualSize := r.capacityGetFunc(className) / (1024 * 1024 * 1024)
			actualCapacity := resource.MustParse(fmt.Sprintf("%dGi", actualSize))
			class, err := r.APIUtil.GetStorageClass(className)
			if err != nil {
				glog.Errorf("Failed to get storage class %q: %v", className, err)
				return true
			}
			classStatus := class.Status
			if classStatus == nil || classStatus.Capacity == nil {
				classStatus = &storage.StorageClassStatus{
					Capacity: &storage.StorageClassCapacity{
						MinVolumeSize: defaultMinVolumeSize,
						TopologyKeys:  []string{common.NodeLabelKey},
						Capacities:    make(map[string]resource.Quantity),
					},
				}
				class.Status = classStatus
			}
			if len(classStatus.Capacity.TopologyKeys) != 1 ||
				classStatus.Capacity.TopologyKeys[0] != common.NodeLabelKey {
				glog.Errorf("Topology Key in storage class %q is not %q, but %v", className, common.NodeLabelKey, classStatus.Capacity.TopologyKeys)
				// TODO: Should we modify the topology key if not match?
				return false
			}
			recordedCapacity, ok := classStatus.Capacity.Capacities[nodeHostName]
			if ok {
				if recordedCapacity.Cmp(actualCapacity) == 0 {
					// Skip if capacity not changed
					return false
				}
			}

			classStatus.Capacity.Capacities[nodeHostName] = actualCapacity
			if _, err := r.APIUtil.UpdateStorageClassStatus(class); err != nil {
				glog.Errorf("Failed to update storage class %q: %v", className, err)
				return true
			}
			return false
		}
		for i := 0; i <= defaultRetryCount; i++ {
			if shouldRetry := workFunc(); !shouldRetry {
				glog.Infof("Capacity reconcile of storage class %q finished", className)
				return
			}
			time.Sleep(defaultRetryWaitDuration)
		}
	}
}
