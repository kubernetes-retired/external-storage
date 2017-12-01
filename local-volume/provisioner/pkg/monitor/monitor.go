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

package monitor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/api/v1/helper"
)

const (
	// DefaultInformerResyncPeriod is the resync period of informer
	DefaultInformerResyncPeriod = 15 * time.Second

	// DefaultMonitorResyncPeriod is the resync period of monitor
	DefaultMonitorResyncPeriod = 1 * time.Minute

	// UpdatePVRetryCount is the retry count of PV updating
	UpdatePVRetryCount = 5

	// UpdatePVInterval is the interval of PV updating
	UpdatePVInterval = 5 * time.Millisecond
)

// marking event related const vars
const (
	MarkPVFailed      = "MarkPVFailed"
	UnMarkPVFailed    = "UnMarkPVFailed"
	MarkPVSucceeded   = "MarkPVSucceeded"
	UnMarkPVSucceeded = "UnMarkPVSucceeded"

	HostPathNotExist  = "HostPathNotExist"
	MisMatchedVolSize = "MisMatchedVolSize"
	NotMountPoint     = "NotMountPoint"

	FirstMarkTime = "FirstMarkTime"
)

// PVUnhealthyKeys stores all the unhealthy marking keys
var PVUnhealthyKeys []string

func init() {
	PVUnhealthyKeys = append(PVUnhealthyKeys, HostPathNotExist)
	PVUnhealthyKeys = append(PVUnhealthyKeys, MisMatchedVolSize)
	PVUnhealthyKeys = append(PVUnhealthyKeys, NotMountPoint)
}

// Monitor checks PVs' health condition and taint them if they are unhealthy
type Monitor struct {
	*common.RuntimeConfig

	volumeLW         cache.ListerWatcher
	volumeController cache.Controller

	localVolumeMap LocalVolumeMap

	hasRun     bool
	hasRunLock *sync.Mutex
}

// NewMonitor creates a monitor object that will scan through
// the configured directories and check volume status
func NewMonitor(config *common.RuntimeConfig) *Monitor {
	monitor := &Monitor{
		RuntimeConfig: config,
		hasRun:        false,
		hasRunLock:    &sync.Mutex{},
	}

	labelOps := metav1.ListOptions{
		LabelSelector: labels.Everything().String(),
	}
	if len(monitor.UserConfig.LabelSelectorForPV) > 0 {
		labelOps.LabelSelector = monitor.UserConfig.LabelSelectorForPV
	}

	monitor.localVolumeMap = NewLocalVolumeMap()

	monitor.volumeLW = &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return config.Client.CoreV1().PersistentVolumes().List(labelOps)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return config.Client.CoreV1().PersistentVolumes().Watch(labelOps)
		},
	}
	_, monitor.volumeController = cache.NewInformer(
		monitor.volumeLW,
		&v1.PersistentVolume{},
		DefaultInformerResyncPeriod,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    monitor.addVolume,
			UpdateFunc: monitor.updateVolume,
			DeleteFunc: monitor.deleteVolume,
		},
	)

	// fill map at first with data from ETCD
	monitor.flushFromETCDFirst()

	return monitor
}

// flushFromETCDFirst fill map with data from etcd at first
func (monitor *Monitor) flushFromETCDFirst() error {
	pvs, err := monitor.Client.CoreV1().PersistentVolumes().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(pvs.Items) == 0 {
		glog.Infof("no pv in ETCD at first")
		return nil
	}

	for _, pv := range pvs.Items {
		monitor.localVolumeMap.AddLocalVolume(&pv)
	}
	return nil
}

func (monitor *Monitor) addVolume(obj interface{}) {
	volume, ok := obj.(*v1.PersistentVolume)
	if !ok {
		glog.Errorf("Expected PersistentVolume but handler received %#v", obj)
		return
	}

	monitor.localVolumeMap.AddLocalVolume(volume)

}

func (monitor *Monitor) updateVolume(oldObj, newObj interface{}) {
	newVolume, ok := newObj.(*v1.PersistentVolume)
	if !ok {
		glog.Errorf("Expected PersistentVolume but handler received %#v", newObj)
		return
	}

	monitor.localVolumeMap.UpdateLocalVolume(newVolume)
}

func (monitor *Monitor) deleteVolume(obj interface{}) {
	volume, ok := obj.(*v1.PersistentVolume)
	if !ok {
		glog.Errorf("Expected PersistentVolume but handler received %#v", obj)
		return
	}

	monitor.localVolumeMap.DeleteLocalVolume(volume)

}

// Run starts all of this controller's control loops
func (monitor *Monitor) Run(stopCh <-chan struct{}) {
	glog.Infof("Starting monitor controller %s!", string(monitor.RuntimeConfig.Name))
	monitor.hasRunLock.Lock()
	monitor.hasRun = true
	monitor.hasRunLock.Unlock()
	go monitor.volumeController.Run(stopCh)

	go monitor.MonitorLocalVolumes()
	<-stopCh
}

// HasRun returns whether the volume controller has Run
func (monitor *Monitor) HasRun() bool {
	monitor.hasRunLock.Lock()
	defer monitor.hasRunLock.Unlock()
	return monitor.hasRun
}

// MonitorLocalVolumes checks local PVs periodically
func (monitor *Monitor) MonitorLocalVolumes() {
	for {
		if monitor.HasRun() {
			pvs := monitor.localVolumeMap.GetPVs()
			for _, pv := range pvs {
				monitor.checkStatus(pv)
			}
		}

		time.Sleep(DefaultMonitorResyncPeriod)
	}
}

// checkStatus checks pv health condition
func (monitor *Monitor) checkStatus(pv *v1.PersistentVolume) {
	// check if PV is local storage
	if pv.Spec.Local == nil {
		glog.Infof("PV: %s is not local storage", pv.Name)
		return
	}
	// check node and pv affinity
	fit, err := CheckNodeAffinity(pv, monitor.Node.Labels)
	if err != nil {
		glog.Errorf("check node affinity error: %v", err)
		return
	}
	if !fit {
		glog.Errorf("pv: %s does not belong to this node: %s", pv.Name, monitor.Node.Name)
		return
	}

	// check if host dir still exists
	mountPath, continueThisCheck := monitor.checkHostDir(pv)
	if !continueThisCheck {
		glog.Errorf("Host dir is modified, PV should be marked")
		return
	}

	// check if it is still a mount point
	continueThisCheck = monitor.checkMountPoint(mountPath, pv)
	if !continueThisCheck {
		glog.Errorf("Retrieving mount points error or %s is not a mount point any more", mountPath)
		return
	}

	// check PV size: PV capacity must not be greater than device capacity and PV used bytes must not be greater that PV capacity
	dir, _ := monitor.VolUtil.IsDir(mountPath)
	if dir {
		monitor.checkPVAndFSSize(mountPath, pv)
	}
	bl, _ := monitor.VolUtil.IsBlock(mountPath)
	if bl {
		monitor.checkPVAndBlockSize(mountPath, pv)
	}
}

func (monitor *Monitor) checkMountPoint(mountPath string, pv *v1.PersistentVolume) bool {
	// Retrieve list of mount points to iterate through discovered paths (aka files) below
	mountPoints, mountPointsErr := monitor.RuntimeConfig.Mounter.List()
	if mountPointsErr != nil {
		glog.Errorf("Error retrieving mount points: %v", mountPointsErr)
		return false
	}
	// Check if mountPath is still a mount point
	for _, mp := range mountPoints {
		if mp.Path == mountPath {
			glog.V(10).Infof("mountPath is still a mount point: %s", mountPath)
			err := monitor.markOrUnmarkPV(pv, NotMountPoint, "yes", false)
			if err != nil {
				glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
			}
			return true
		}
	}

	glog.V(6).Infof("mountPath is not a mount point any more: %s", mountPath)
	err := monitor.markOrUnmarkPV(pv, NotMountPoint, "yes", true)
	if err != nil {
		glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
	}
	return false

}

func (monitor *Monitor) checkHostDir(pv *v1.PersistentVolume) (mountPath string, continueThisCheck bool) {
	var err error
	for _, config := range monitor.DiscoveryMap {
		if strings.Contains(pv.Spec.Local.Path, config.HostDir) {
			mountPath, err = common.GetContainerPath(pv, config)
			if err != nil {
				glog.Errorf("get container path error: %v", err)
			}
			break
		}
	}
	if len(mountPath) == 0 {
		// can not find mount path, this may because: admin modify config(hostpath)
		// mark PV and send a event
		err = monitor.markOrUnmarkPV(pv, HostPathNotExist, "yes", true)
		if err != nil {
			glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
		}
		return
	}
	dir, dirErr := monitor.VolUtil.IsDir(mountPath)
	bl, blErr := monitor.VolUtil.IsBlock(mountPath)
	if !dir && !bl && (dirErr != nil || blErr != nil) {
		// mountPath does not exist or is not a directory
		// mark PV and send a event
		err = monitor.markOrUnmarkPV(pv, HostPathNotExist, "yes", true)
		if err != nil {
			glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
		}
		return
	}
	continueThisCheck = true
	// unmark PV if it was marked before
	err = monitor.markOrUnmarkPV(pv, HostPathNotExist, "yes", false)
	if err != nil {
		glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
	}
	return

}

func (monitor *Monitor) checkPVAndFSSize(mountPath string, pv *v1.PersistentVolume) {
	capacityByte, err := monitor.VolUtil.GetFsCapacityByte(mountPath)
	if err != nil {
		glog.Errorf("Path %q fs stats error: %v", mountPath, err)
		return
	}
	// actually if PV is provisioned by provisioner, the two values must be equal, but the PV may be
	// created manually, so the PV capacity must not be greater than FS capacity
	storage := pv.Spec.Capacity[v1.ResourceStorage]
	if util.RoundDownCapacityPretty(capacityByte) < storage.Value() {
		// mark PV and send a event
		err = monitor.markOrUnmarkPV(pv, MisMatchedVolSize, "yes", true)
		if err != nil {
			glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
		}
		return
	}
	// TODO: make sure that PV used bytes is not greater that PV capacity ?

	// unmark PV if it was marked before
	err = monitor.markOrUnmarkPV(pv, MisMatchedVolSize, "yes", false)
	if err != nil {
		glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
	}
	return

}

func (monitor *Monitor) checkPVAndBlockSize(mountPath string, pv *v1.PersistentVolume) {
	capacityByte, err := monitor.VolUtil.GetBlockCapacityByte(mountPath)
	if err != nil {
		glog.Errorf("Path %q block stats error: %v", mountPath, err)
		return
	}
	// actually if PV is provisioned by provisioner, the two values must be equal, but the PV may be
	// created manually, so the PV capacity must not be greater than block device capacity
	storage := pv.Spec.Capacity[v1.ResourceStorage]
	if util.RoundDownCapacityPretty(capacityByte) < storage.Value() {
		// mark PV and send a event
		err = monitor.markOrUnmarkPV(pv, MisMatchedVolSize, "yes", true)
		if err != nil {
			glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
		}
		return
	}
	// TODO: make sure that PV used bytes is not greater that PV capacity ?

	// unmark PV if it was marked before
	err = monitor.markOrUnmarkPV(pv, MisMatchedVolSize, "yes", false)
	if err != nil {
		glog.Errorf("mark PV: %s failed, err: %v", pv.Name, err)
	}
	return
}

// CheckNodeAffinity looks at the PV node affinity, and checks if the node has the same corresponding labels
// This ensures that we don't mount a volume that doesn't belong to this node
func CheckNodeAffinity(pv *v1.PersistentVolume, nodeLabels map[string]string) (bool, error) {
	affinity, err := helper.GetStorageNodeAffinityFromAnnotation(pv.Annotations)
	if err != nil {
		return false, fmt.Errorf("error getting storage node affinity: %v", err)
	}
	if affinity == nil {
		return false, nil
	}

	if affinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		terms := affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		glog.V(10).Infof("Match for RequiredDuringSchedulingIgnoredDuringExecution node selector terms %+v", terms)
		for _, term := range terms {
			selector, err := helper.NodeSelectorRequirementsAsSelector(term.MatchExpressions)
			if err != nil {
				return false, fmt.Errorf("failed to parse MatchExpressions: %v", err)
			}
			if !selector.Matches(labels.Set(nodeLabels)) {
				return false, fmt.Errorf("NodeSelectorTerm %+v does not match node labels", term.MatchExpressions)
			}
		}
	}
	return true, nil
}

// markPV marks PV by adding annotation
func (monitor *Monitor) markOrUnmarkPV(pv *v1.PersistentVolume, ann, value string, mark bool) error {
	// The volume from method args can be pointing to watcher cache. We must not
	// modify these, therefore create a copy.
	volumeClone := pv.DeepCopy()
	var eventMes string

	if mark {
		// mark PV
		_, ok := volumeClone.ObjectMeta.Annotations[ann]
		if ok {
			glog.V(10).Infof("PV: %s is already marked with ann: %s", volumeClone.Name, ann)
			return nil
		}
		metav1.SetMetaDataAnnotation(&volumeClone.ObjectMeta, ann, value)
		_, ok = volumeClone.ObjectMeta.Annotations[FirstMarkTime]
		if !ok {
			firstMarkTime := time.Now()
			metav1.SetMetaDataAnnotation(&volumeClone.ObjectMeta, FirstMarkTime, firstMarkTime.String())
		}
	} else {
		// unmark PV
		_, ok := volumeClone.ObjectMeta.Annotations[ann]
		if !ok {
			glog.V(10).Infof("PV: %s is not marked with ann: %s", volumeClone.Name, ann)
			return nil
		}
		delete(volumeClone.ObjectMeta.Annotations, ann)
		var hasOtherMarkKeys bool
		for _, key := range PVUnhealthyKeys {
			if _, ok = volumeClone.ObjectMeta.Annotations[key]; ok {
				hasOtherMarkKeys = true
				break
			}
		}
		if !hasOtherMarkKeys {
			delete(volumeClone.ObjectMeta.Annotations, FirstMarkTime)
		}

	}

	var err error
	var newVol *v1.PersistentVolume
	// Try to update the PV object several times
	for i := 0; i < UpdatePVRetryCount; i++ {
		glog.V(4).Infof("try to update PV: %s", pv.Name)
		newVol, err = monitor.APIUtil.UpdatePV(volumeClone)
		if err != nil {
			glog.V(4).Infof("updating PersistentVolume[%s] failed: %v", volumeClone.Name, err)
			continue
		}
		monitor.localVolumeMap.UpdateLocalVolume(newVol)
		glog.V(4).Infof("updating PersistentVolume[%s] successfully", newVol.Name)
		if mark {
			eventMes = "Mark PV successfully with annotation key: " + ann
			monitor.Recorder.Event(pv, v1.EventTypeNormal, MarkPVSucceeded, eventMes)
		} else {
			eventMes = "UnMark PV successfully, removed annotation key: " + ann
			monitor.Recorder.Event(pv, v1.EventTypeNormal, UnMarkPVSucceeded, "UnMark PV successfully")
		}
		time.Sleep(UpdatePVInterval)
		return nil
	}

	if mark {
		eventMes = "Failed to Mark PV with annotation key: " + ann
		monitor.Recorder.Event(pv, v1.EventTypeWarning, MarkPVFailed, "Failed to Mark PV")
	} else {
		eventMes = "Failed to UnMark PV, attempt to remove annotation key: " + ann
		monitor.Recorder.Event(pv, v1.EventTypeWarning, UnMarkPVFailed, "Failed to UnMark PV")
	}
	return err
}
