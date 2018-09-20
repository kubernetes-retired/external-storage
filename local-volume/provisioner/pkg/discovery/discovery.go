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
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics"

	esUtil "github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"k8s.io/api/core/v1"
	storagev1listers "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/tools/cache"
)

// Discoverer finds available volumes and creates PVs for them
// It looks for volumes in the directories specified in the discoveryMap
type Discoverer struct {
	*common.RuntimeConfig
	Labels map[string]string
	// ProcTable is a reference to running processes so that we can prevent PV from being created while
	// it is being cleaned
	CleanupTracker  *deleter.CleanupStatusTracker
	nodeAffinityAnn string
	nodeAffinity    *v1.VolumeNodeAffinity
	classLister     storagev1listers.StorageClassLister
}

// NewDiscoverer creates a Discoverer object that will scan through
// the configured directories and create local PVs for any new directories found
func NewDiscoverer(config *common.RuntimeConfig, cleanupTracker *deleter.CleanupStatusTracker) (*Discoverer, error) {
	sharedInformer := config.InformerFactory.Storage().V1().StorageClasses()
	sharedInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		// We don't need an actual event handler for StorageClasses,
		// but we must pass a non-nil one to cache.NewInformer()
		AddFunc:    nil,
		UpdateFunc: nil,
		DeleteFunc: nil,
	})

	labelMap := make(map[string]string)
	for _, labelName := range config.NodeLabelsForPV {
		labelVal, ok := config.Node.Labels[labelName]
		if ok {
			labelMap[labelName] = labelVal
		}
	}

	if config.UseAlphaAPI {
		nodeAffinity, err := generateNodeAffinity(config.Node)
		if err != nil {
			return nil, fmt.Errorf("Failed to generate node affinity: %v", err)
		}
		tmpAnnotations := map[string]string{}
		err = StorageNodeAffinityToAlphaAnnotation(tmpAnnotations, nodeAffinity)
		if err != nil {
			return nil, fmt.Errorf("Failed to convert node affinity to alpha annotation: %v", err)
		}
		return &Discoverer{
			RuntimeConfig:   config,
			Labels:          labelMap,
			CleanupTracker:  cleanupTracker,
			classLister:     sharedInformer.Lister(),
			nodeAffinityAnn: tmpAnnotations[common.AlphaStorageNodeAffinityAnnotation]}, nil
	}

	volumeNodeAffinity, err := generateVolumeNodeAffinity(config.Node)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate volume node affinity: %v", err)
	}

	return &Discoverer{
		RuntimeConfig:  config,
		Labels:         labelMap,
		CleanupTracker: cleanupTracker,
		classLister:    sharedInformer.Lister(),
		nodeAffinity:   volumeNodeAffinity}, nil
}

func generateNodeAffinity(node *v1.Node) (*v1.NodeAffinity, error) {
	if node.Labels == nil {
		return nil, fmt.Errorf("Node does not have labels")
	}
	nodeValue, found := node.Labels[common.NodeLabelKey]
	if !found {
		return nil, fmt.Errorf("Node does not have expected label %s", common.NodeLabelKey)
	}

	return &v1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
			NodeSelectorTerms: []v1.NodeSelectorTerm{
				{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      common.NodeLabelKey,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{nodeValue},
						},
					},
				},
			},
		},
	}, nil
}

func generateVolumeNodeAffinity(node *v1.Node) (*v1.VolumeNodeAffinity, error) {
	if node.Labels == nil {
		return nil, fmt.Errorf("Node does not have labels")
	}
	nodeValue, found := node.Labels[common.NodeLabelKey]
	if !found {
		return nil, fmt.Errorf("Node does not have expected label %s", common.NodeLabelKey)
	}

	return &v1.VolumeNodeAffinity{
		Required: &v1.NodeSelector{
			NodeSelectorTerms: []v1.NodeSelectorTerm{
				{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      common.NodeLabelKey,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{nodeValue},
						},
					},
				},
			},
		},
	}, nil
}

// DiscoverLocalVolumes reads the configured discovery paths, and creates PVs for the new volumes
func (d *Discoverer) DiscoverLocalVolumes() {
	for class, config := range d.DiscoveryMap {
		d.discoverVolumesAtPath(class, config)
	}
}

func (d *Discoverer) getReclaimPolicyFromStorageClass(name string) (v1.PersistentVolumeReclaimPolicy, error) {
	class, err := d.classLister.Get(name)
	if err != nil {
		return "", err
	}
	if class.ReclaimPolicy != nil {
		return *class.ReclaimPolicy, nil
	}
	return v1.PersistentVolumeReclaimDelete, nil
}

func (d *Discoverer) discoverVolumesAtPath(class string, config common.MountConfig) {
	glog.V(7).Infof("Discovering volumes at hostpath %q, mount path %q for storage class %q", config.HostDir, config.MountDir, class)

	reclaimPolicy, err := d.getReclaimPolicyFromStorageClass(class)
	if err != nil {
		glog.Errorf("Failed to get ReclaimPolicy from storage class %q: %v", class, err)
		return
	}

	if reclaimPolicy != v1.PersistentVolumeReclaimRetain && reclaimPolicy != v1.PersistentVolumeReclaimDelete {
		glog.Errorf("Unsupported ReclaimPolicy %q from storage class %q, supported policy are Retain and Delete.", reclaimPolicy, class)
		return
	}

	files, err := d.VolUtil.ReadDir(config.MountDir)
	if err != nil {
		glog.Errorf("Error reading directory: %v", err)
		return
	}

	// Retreive list of mount points to iterate through discovered paths (aka files) below
	mountPoints, mountPointsErr := d.RuntimeConfig.Mounter.List()
	if mountPointsErr != nil {
		glog.Errorf("Error retreiving mountpoints: %v", err)
		return
	}
	// Put mount moints into set for faster checks below
	type empty struct{}
	mountPointMap := make(map[string]empty)
	for _, mp := range mountPoints {
		mountPointMap[mp.Path] = empty{}
	}

	for _, file := range files {
		startTime := time.Now()
		filePath := filepath.Join(config.MountDir, file)
		volMode, err := d.getVolumeMode(filePath)
		if err != nil {
			glog.Error(err)
			continue
		}
		// Check if PV already exists for it
		pvName := generatePVName(file, d.Node.Name, class)
		pv, exists := d.Cache.GetPV(pvName)
		if exists {
			if volMode == v1.PersistentVolumeBlock && (pv.Spec.VolumeMode == nil ||
				*pv.Spec.VolumeMode != v1.PersistentVolumeBlock) {
				errStr := fmt.Sprintf("Incorrect Volume Mode: PV %q (path %q) was not created in block mode. "+
					"Please check if BlockVolume features gate has been enabled for the cluster.", pvName, filePath)
				glog.Error(errStr)
				d.Recorder.Eventf(pv, v1.EventTypeWarning, common.EventVolumeFailedDelete, errStr)
			}
			continue
		}

		usejob := false
		if volMode == v1.PersistentVolumeBlock {
			usejob = d.RuntimeConfig.UseJobForCleaning
		}
		if d.CleanupTracker.InProgress(pvName, usejob) {
			glog.Infof("PV %s is still being cleaned, not going to recreate it", pvName)
			continue
		}

		var capacityByte int64
		switch volMode {
		case v1.PersistentVolumeBlock:
			capacityByte, err = d.VolUtil.GetBlockCapacityByte(filePath)
			if err != nil {
				glog.Errorf("Path %q block stats error: %v", filePath, err)
				continue
			}
		case v1.PersistentVolumeFilesystem:
			// Validate that this path is an actual mountpoint
			if _, isMntPnt := mountPointMap[filePath]; isMntPnt == false {
				glog.Errorf("Path %q is not an actual mountpoint", filePath)
				continue
			}
			capacityByte, err = d.VolUtil.GetFsCapacityByte(filePath)
			if err != nil {
				glog.Errorf("Path %q fs stats error: %v", filePath, err)
				continue
			}
		default:
			glog.Errorf("Path %q has unexpected volume type %q", filePath, volMode)
			continue
		}

		d.createPV(file, class, reclaimPolicy, config, capacityByte, volMode, startTime)
	}
}

func (d *Discoverer) getVolumeMode(fullPath string) (v1.PersistentVolumeMode, error) {
	isdir, errdir := d.VolUtil.IsDir(fullPath)
	if isdir {
		return v1.PersistentVolumeFilesystem, nil
	}
	// check for Block before returning errdir
	isblk, errblk := d.VolUtil.IsBlock(fullPath)
	if isblk {
		return v1.PersistentVolumeBlock, nil
	}

	if errdir == nil && errblk == nil {
		return "", fmt.Errorf("Skipping file %q: not a directory nor block device", fullPath)
	}

	// report the first error found
	if errdir != nil {
		return "", fmt.Errorf("Directory check for %q failed: %s", fullPath, errdir)
	}
	return "", fmt.Errorf("Block device check for %q failed: %s", fullPath, errblk)
}

func generatePVName(file, node, class string) string {
	h := fnv.New32a()
	h.Write([]byte(file))
	h.Write([]byte(node))
	h.Write([]byte(class))
	// This is the FNV-1a 32-bit hash
	return fmt.Sprintf("local-pv-%x", h.Sum32())
}

func (d *Discoverer) createPV(file, class string, reclaimPolicy v1.PersistentVolumeReclaimPolicy, config common.MountConfig, capacityByte int64, volMode v1.PersistentVolumeMode, startTime time.Time) {
	pvName := generatePVName(file, d.Node.Name, class)
	outsidePath := filepath.Join(config.HostDir, file)

	glog.Infof("Found new volume of volumeMode %q at host path %q with capacity %d, creating Local PV %q",
		volMode, outsidePath, capacityByte, pvName)

	localPVConfig := &common.LocalPVConfig{
		Name:            pvName,
		HostPath:        outsidePath,
		Capacity:        roundDownCapacityPretty(capacityByte),
		StorageClass:    class,
		ReclaimPolicy:   reclaimPolicy,
		ProvisionerName: d.Name,
		VolumeMode:      volMode,
		Labels:          d.Labels,
	}

	if d.UseAlphaAPI {
		localPVConfig.UseAlphaAPI = true
		localPVConfig.AffinityAnn = d.nodeAffinityAnn
	} else {
		localPVConfig.NodeAffinity = d.nodeAffinity
	}

	pvSpec := common.CreateLocalPVSpec(localPVConfig)

	_, err := d.APIUtil.CreatePV(pvSpec)
	if err != nil {
		glog.Errorf("Error creating PV %q for volume at %q: %v", pvName, outsidePath, err)
		return
	}
	glog.Infof("Created PV %q for volume at %q", pvName, outsidePath)
	mode := string(volMode)
	metrics.PersistentVolumeDiscoveryTotal.WithLabelValues(mode).Inc()
	metrics.PersistentVolumeDiscoveryDurationSeconds.WithLabelValues(mode).Observe(time.Since(startTime).Seconds())
}

// Round down the capacity to an easy to read value.
func roundDownCapacityPretty(capacityBytes int64) int64 {

	easyToReadUnitsBytes := []int64{esUtil.GiB, esUtil.MiB}

	// Round down to the nearest easy to read unit
	// such that there are at least 10 units at that size.
	for _, easyToReadUnitBytes := range easyToReadUnitsBytes {
		// Round down the capacity to the nearest unit.
		size := capacityBytes / easyToReadUnitBytes
		if size >= 10 {
			return size * easyToReadUnitBytes
		}
	}
	return capacityBytes
}

// GetStorageNodeAffinityFromAnnotation gets the json serialized data from PersistentVolume.Annotations
// and converts it to the NodeAffinity type in core.
func GetStorageNodeAffinityFromAnnotation(annotations map[string]string) (*v1.NodeAffinity, error) {
	if len(annotations) > 0 && annotations[common.AlphaStorageNodeAffinityAnnotation] != "" {
		var affinity v1.NodeAffinity
		err := json.Unmarshal([]byte(annotations[common.AlphaStorageNodeAffinityAnnotation]), &affinity)
		if err != nil {
			return nil, err
		}
		return &affinity, nil
	}
	return nil, nil
}

// StorageNodeAffinityToAlphaAnnotation converts NodeAffinity type to Alpha annotation for use in PersistentVolumes
func StorageNodeAffinityToAlphaAnnotation(annotations map[string]string, affinity *v1.NodeAffinity) error {
	if affinity == nil {
		return nil
	}

	json, err := json.Marshal(*affinity)
	if err != nil {
		return err
	}
	annotations[common.AlphaStorageNodeAffinityAnnotation] = string(json)
	return nil
}
