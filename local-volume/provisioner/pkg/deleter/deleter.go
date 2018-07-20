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

package deleter

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

// CleanupState indicates the state of the cleanup process.
type CleanupState int

const (
	// CSUnknown State of the cleanup is unknown.
	CSUnknown CleanupState = iota + 1
	// CSNotFound No cleanup process was found.
	CSNotFound
	// CSRunning Cleanup process is still running.
	CSRunning
	// CSFailed Cleanup process has ended in failure.
	CSFailed
	// CSSucceeded Cleanup process has ended successfully.
	CSSucceeded
)

// Deleter handles PV cleanup and object deletion
// For file-based volumes, it deletes the contents of the directory
type Deleter struct {
	*common.RuntimeConfig
	CleanupStatus *CleanupStatusTracker
}

// NewDeleter creates a Deleter object to handle the cleanup and deletion of local PVs
// allocated by this provisioner
func NewDeleter(config *common.RuntimeConfig, cleanupTracker *CleanupStatusTracker) *Deleter {
	return &Deleter{
		RuntimeConfig: config,
		CleanupStatus: cleanupTracker,
	}
}

// DeletePVs will scan through all the existing PVs that are released, and cleanup and
// delete them
func (d *Deleter) DeletePVs() {
	for _, pv := range d.Cache.ListPVs() {
		if pv.Status.Phase != v1.VolumeReleased {
			continue
		}
		name := pv.Name
		switch pv.Spec.PersistentVolumeReclaimPolicy {
		case v1.PersistentVolumeReclaimRetain:
			glog.V(4).Infof("reclaimVolume[%s]: policy is Retain, nothing to do", name)
		case v1.PersistentVolumeReclaimRecycle:
			glog.V(4).Infof("reclaimVolume[%s]: policy is Recycle which is not supported", name)
			d.RuntimeConfig.Recorder.Eventf(pv, v1.EventTypeWarning, "VolumeUnsupportedReclaimPolicy", "Volume has unsupported PersistentVolumeReclaimPolicy: Recycle")
		case v1.PersistentVolumeReclaimDelete:
			glog.V(4).Infof("reclaimVolume[%s]: policy is Delete", name)
			// Cleanup volume
			err := d.deletePV(pv)
			if err != nil {
				mode, runjob := d.getVolModeAndRunJob(pv)
				deleteType := metrics.DeleteTypeProcess
				if runjob {
					deleteType = metrics.DeleteTypeJob
				}
				metrics.PersistentVolumeDeleteFailedTotal.WithLabelValues(string(mode), deleteType).Inc()
				cleaningLocalPVErr := fmt.Errorf("Error cleaning PV %q: %v", name, err.Error())
				d.RuntimeConfig.Recorder.Eventf(pv, v1.EventTypeWarning, common.EventVolumeFailedDelete, cleaningLocalPVErr.Error())
				glog.Error(err)
				continue
			}
		default:
			// Unknown PersistentVolumeReclaimPolicy
			d.RuntimeConfig.Recorder.Eventf(pv, v1.EventTypeWarning, "VolumeUnknownReclaimPolicy", "Volume has unrecognized PersistentVolumeReclaimPolicy")
		}
	}
}

func (d *Deleter) getVolModeAndRunJob(pv *v1.PersistentVolume) (v1.PersistentVolumeMode, bool) {
	// Default is filesystem mode, so even if volume mode is not specified, mode should be filesystem.
	volMode := v1.PersistentVolumeFilesystem
	if pv.Spec.VolumeMode != nil && *pv.Spec.VolumeMode == v1.PersistentVolumeBlock {
		volMode = v1.PersistentVolumeBlock
	}
	return volMode, d.RuntimeConfig.UseJobForCleaning
}

func (d *Deleter) deletePV(pv *v1.PersistentVolume) error {
	if pv.Spec.Local == nil {
		return fmt.Errorf("Unsupported volume type")
	}

	config, ok := d.DiscoveryMap[pv.Spec.StorageClassName]
	if !ok {
		return fmt.Errorf("Unknown storage class name %s", pv.Spec.StorageClassName)
	}

	mountPath, err := common.GetContainerPath(pv, config)
	if err != nil {
		return err
	}

	volMode, runjob := d.getVolModeAndRunJob(pv)

	// Exit if cleaning is still in progress.
	if d.CleanupStatus.InProgress(pv.Name, runjob) {
		return nil
	}

	// Check if cleaning was just completed.
	state, startTime, err := d.CleanupStatus.RemoveStatus(pv.Name, runjob)
	if err != nil {
		return err
	}

	switch state {
	case CSSucceeded:
		// Found a completed cleaning entry
		glog.Infof("Deleting pv %s after successful cleanup", pv.Name)
		if err = d.APIUtil.DeletePV(pv.Name); err != nil {
			if !errors.IsNotFound(err) {
				d.RuntimeConfig.Recorder.Eventf(pv, v1.EventTypeWarning, common.EventVolumeFailedDelete,
					err.Error())
				return fmt.Errorf("Error deleting PV %q: %v", pv.Name, err.Error())
			}
		}
		mode := string(volMode)
		deleteType := metrics.DeleteTypeProcess
		if runjob {
			deleteType = metrics.DeleteTypeJob
		}
		metrics.PersistentVolumeDeleteTotal.WithLabelValues(mode, deleteType).Inc()
		if startTime != nil {
			var capacityBytes int64
			if capacity, ok := pv.Spec.Capacity[v1.ResourceStorage]; ok {
				capacityBytes = capacity.Value()
			}
			capacityBreakDown := metrics.CapacityBreakDown(capacityBytes)
			cleanupCommand := ""
			if len(config.BlockCleanerCommand) > 0 {
				cleanupCommand = config.BlockCleanerCommand[0]
			}
			metrics.PersistentVolumeDeleteDurationSeconds.WithLabelValues(mode, deleteType, capacityBreakDown, cleanupCommand).Observe(time.Since(*startTime).Seconds())
		}
		return nil
	case CSFailed:
		glog.Infof("Cleanup for pv %s failed. Restarting cleanup", pv.Name)
	case CSNotFound:
		glog.Infof("Start cleanup for pv %s", pv.Name)
	default:
		return fmt.Errorf("Unexpected state %d for pv %s", state, pv.Name)
	}

	if volMode == v1.PersistentVolumeBlock {
		if len(config.BlockCleanerCommand) < 1 {
			return fmt.Errorf("Blockcleaner command was empty for pv %q mountPath %s but mount dir is %s", pv.Name,
				mountPath, config.MountDir)
		}
	}

	if runjob {
		// If we are dealing with block volumes and using jobs based cleaning for it.
		return d.runJob(pv, volMode, mountPath, config)
	}

	return d.runProcess(pv, volMode, mountPath, config)
}

func (d *Deleter) runProcess(pv *v1.PersistentVolume, volMode v1.PersistentVolumeMode, mountPath string,
	config common.MountConfig) error {
	// Run as exec script.
	err := d.CleanupStatus.ProcTable.MarkRunning(pv.Name)
	if err != nil {
		return err
	}

	go d.asyncCleanPV(pv, volMode, mountPath, config)
	return nil
}

func (d *Deleter) asyncCleanPV(pv *v1.PersistentVolume, volMode v1.PersistentVolumeMode, mountPath string,
	config common.MountConfig) {

	err := d.cleanPV(pv, volMode, mountPath, config)
	if err != nil {
		glog.Error(err)
		// Set process as failed.
		if err := d.CleanupStatus.ProcTable.MarkFailed(pv.Name); err != nil {
			glog.Error(err)
		}
		return
	}
	// Set process as succeeded.
	if err := d.CleanupStatus.ProcTable.MarkSucceeded(pv.Name); err != nil {
		glog.Error(err)
	}
}

func (d *Deleter) cleanPV(pv *v1.PersistentVolume, volMode v1.PersistentVolumeMode, mountPath string,
	config common.MountConfig) error {
	// Make absolutely sure here that we are not deleting anything outside of mounted dir
	if !strings.HasPrefix(mountPath, config.MountDir) {
		return fmt.Errorf("Unexpected error pv %q mountPath %s but mount dir is %s", pv.Name, mountPath,
			config.MountDir)
	}

	var err error
	switch volMode {
	case v1.PersistentVolumeFilesystem:
		err = d.cleanFilePV(pv, mountPath, config)
	case v1.PersistentVolumeBlock:
		err = d.cleanBlockPV(pv, mountPath, config)
	default:
		err = fmt.Errorf("Unexpected volume mode %q for deleting path %q", volMode, pv.Spec.Local.Path)
	}
	return err
}

func (d *Deleter) cleanFilePV(pv *v1.PersistentVolume, mountPath string, config common.MountConfig) error {
	glog.Infof("Deleting PV file volume %q contents at hostpath %q, mountpath %q", pv.Name, pv.Spec.Local.Path,
		mountPath)

	return d.VolUtil.DeleteContents(mountPath)
}

func (d *Deleter) cleanBlockPV(pv *v1.PersistentVolume, blkdevPath string, config common.MountConfig) error {
	cleaningInfo := fmt.Errorf("Starting cleanup of Block PV %q, this may take a while", pv.Name)
	d.RuntimeConfig.Recorder.Eventf(pv, v1.EventTypeNormal, common.VolumeDelete, cleaningInfo.Error())
	glog.Infof("Deleting PV block volume %q device hostpath %q, mountpath %q", pv.Name, pv.Spec.Local.Path,
		blkdevPath)

	err := d.execScript(pv.Name, blkdevPath, config.BlockCleanerCommand[0], config.BlockCleanerCommand[1:]...)
	if err != nil {
		glog.Error(err)
		return err
	}
	glog.Infof("Completed cleanup of pv %q", pv.Name)

	return nil
}

func (d *Deleter) execScript(pvName string, blkdevPath string, exe string, exeArgs ...string) error {
	cmd := exec.Command(exe, exeArgs...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", common.LocalPVEnv, blkdevPath))
	var wg sync.WaitGroup
	// Wait for stderr & stdout  go routines
	wg.Add(2)

	outReader, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	go func() {
		defer wg.Done()
		outScanner := bufio.NewScanner(outReader)
		for outScanner.Scan() {
			outstr := outScanner.Text()
			glog.Infof("Cleanup pv %q: StdoutBuf - %q", pvName, outstr)
		}
	}()

	errReader, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	go func() {
		defer wg.Done()
		errScanner := bufio.NewScanner(errReader)
		for errScanner.Scan() {
			errstr := errScanner.Text()
			glog.Infof("Cleanup pv %q: StderrBuf - %q", pvName, errstr)
		}
	}()

	err = cmd.Start()
	if err != nil {
		return err
	}

	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		return err
	}

	return nil
}

// runJob runs a cleaning job.
// The advantages of using a Job to do block cleaning (which is a process that can take several hours) is as follows
// 1) By naming the job based on the specific name of the volume, one ensures that only one instance of a cleaning
//    job will be active for any given volume. Any attempt to create another will fail due to name collision. This
//    avoids any concurrent cleaning problems.
// 2) The above approach also ensures that we don't accidentally create a new PV when a cleaning job is in progress.
//    Even if a user accidentally deletes the PV, the presence of the cleaning job would prevent the provisioner from
//    attempting to re-create it. This would be the case even if the Daemonset had two provisioners running on the same
//    host (which can sometimes happen as the Daemonset controller follows "at least one" semantics).
// 3) Admins get transparency on what is going on with a released volume by just running kubectl commands
//    to check for any corresponding cleaning job for a given volume and looking into its progress or failure.
//
// To achieve these advantages, the provisioner names the cleaning job with a constant name based on the PV name.
// If a job completes successfully, then the job is first deleted and then the cleaned PV (to enable its rediscovery).
// A failed Job is left "as is" (after a few retries to execute) for admins to intervene/debug and resolve. This is the
// safest thing to do in this scenario as it is even in a non-Job based approach. Please note that for successful jobs,
// deleting it does delete the logs of the job run. This is probably an acceptable initial implementation as the logs
// of successful run are not as interesting. Long term, we might want to fetch the logs of the successful Jobs too,
// before deleting them, but for the initial implementation we will keep things simple and perhaps decide the
// enhancement based on user feedback.
func (d *Deleter) runJob(pv *v1.PersistentVolume, volMode v1.PersistentVolumeMode, mountPath string, config common.MountConfig) error {
	if d.JobContainerImage == "" {
		return fmt.Errorf("cannot run cleanup job without specifying job image name in the environment variable")
	}
	job, err := NewCleanupJob(pv, volMode, d.JobContainerImage, d.Node.Name, d.Namespace, mountPath, config)
	if err != nil {
		return err
	}
	return d.RuntimeConfig.APIUtil.CreateJob(job)
}

// CleanupStatusTracker tracks cleanup processes that are either process based or jobs based.
type CleanupStatusTracker struct {
	ProcTable     ProcTable
	JobController JobController
}

// InProgress returns true if the cleaning for the specified PV is in progress.
func (c *CleanupStatusTracker) InProgress(pvName string, isJob bool) bool {
	if isJob {
		return c.JobController.IsCleaningJobRunning(pvName)
	}
	return c.ProcTable.IsRunning(pvName)
}

// RemoveStatus removes and returns the status and start time of a completed cleaning process.
// The method returns an error if the process has not yet completed.
func (c *CleanupStatusTracker) RemoveStatus(pvName string, isJob bool) (CleanupState, *time.Time, error) {
	if isJob {
		return c.JobController.RemoveJob(pvName)
	}
	return c.ProcTable.RemoveEntry(pvName)
}
