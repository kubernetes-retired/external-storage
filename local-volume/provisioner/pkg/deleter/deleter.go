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
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/pkg/api/v1"
)

const (
	pollInterval = 1 * time.Second
	// TODO: is this too fast?
	waitForDeleteTimeout = 1 * time.Minute
)

// Deleter handles PV cleanup and object deletion
// For file-based volumes, it deletes the contents of the directory
type Deleter struct {
	*common.RuntimeConfig
}

func NewDeleter(config *common.RuntimeConfig) *Deleter {
	return &Deleter{RuntimeConfig: config}
}

func (d *Deleter) DeletePVs() {
	deletedPVs := []string{}
	for _, pv := range d.Cache.ListPVs() {
		if pv.Status.Phase == v1.VolumeReleased {
			name := pv.Name
			glog.Infof("Deleting PV %q", name)

			// Cleanup volume
			err := d.cleanupPV(pv)
			if err != nil {
				// TODO: Log event on PV
				glog.Errorf("Error cleaning PV %q: %v", name, err.Error())
				continue
			}

			// Remove API object
			err = d.APIUtil.DeletePV(name)
			if err != nil {
				// TODO: Log event on PV
				// TODO: Does delete return an error if object has already been deleted?
				glog.Errorf("Error deleting PV %q: %v", name, err.Error())
				continue
			}

			deletedPVs = append(deletedPVs, name)
			glog.Infof("Deleted PV %q", name)
		}
	}

	// Wait for informer to delete PV objects from cache so we don't try to clean it up again.
	for _, name := range deletedPVs {
		err := wait.Poll(pollInterval, waitForDeleteTimeout, func() (bool, error) {
			if d.Cache.PVExists(name) {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			glog.Errorf("PV %q not deleted from cache after %v", name, waitForDeleteTimeout)
		}
	}
}

func (d *Deleter) cleanupPV(pv *v1.PersistentVolume) error {
	if pv.Spec.Local == nil {
		return fmt.Errorf("Unsupported volume type")
	}

	specPath := pv.Spec.Local.Path
	relativePath, err := filepath.Rel(d.HostDir, specPath)
	if err != nil {
		return fmt.Errorf("Could not get relative path: %v", err)
	}

	mountPath := filepath.Join(d.MountDir, relativePath)

	glog.Infof("Deleting PV %q contents at hostpath %q, mountpath %q", pv.Name, specPath, mountPath)
	return d.VolUtil.DeleteContents(mountPath)
}
