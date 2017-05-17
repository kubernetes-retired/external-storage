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
	"path/filepath"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/types"

	"k8s.io/client-go/pkg/api/v1"
)

type Deleter interface {
	// Cleanup and delete all its owned PVs that have been released
	DeletePVs()
}

type deleter struct {
	*types.RuntimeConfig
}

func NewDeleter(config *types.RuntimeConfig) Deleter {
	return &deleter{RuntimeConfig: config}
}

func (d *deleter) DeletePVs() {
	for _, pv := range d.Cache.ListPVs() {
		if pv.Status.Phase == v1.VolumeReleased {
			name := pv.Name
			glog.Infof("Deleting PV: %v\n", name)

			// Cleanup volume
			err := d.cleanupPV(pv)
			if err != nil {
				// TODO: Log event on PV
				glog.Errorf("Error cleaning PV: %v\n", err.Error())
				continue
			}

			// Remove API object
			err = d.APIUtil.DeletePV(name)
			if err != nil {
				// TODO: Log event on PV
				glog.Errorf("Error reading directory: %v\n", err.Error())
				continue
			}

			glog.Infof("Done deleting PV: %v\n", name)
			err = d.Cache.DeletePV(name)
			if err != nil {
				glog.Errorf("Error deleting PV %q from cache: %v\n", name, err.Error())
			}
		}
	}
}

func (d *deleter) cleanupPV(pv *v1.PersistentVolume) error {
	// path := pv.Spec.Local.Path
	// TODO: Need to extract the hostDir from the spec path, and replace with mountdir
	path := "TODO-PLACEHOLDER"
	fullPath := filepath.Join(d.MountDir, path)
	glog.Infof("Deleting contents at %q", fullPath)

	return d.VolUtil.DeleteContents(fullPath)
}
