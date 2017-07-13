/*
Copyright 2016 The Kubernetes Authors.

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

package volume

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
)

// Delete removes the directory that was created by Provision backing the given
// PV and removes its export from the NFS server.
func (p *nfsProvisioner) Delete(volume *v1.PersistentVolume) error {
	// Ignore the call if this provisioner was not the one to provision the
	// volume. It doesn't even attempt to delete it, so it's neither a success
	// (nil error) nor failure (any other error)
	provisioned, err := p.provisioned(volume)
	if err != nil {
		return fmt.Errorf("error determining if this provisioner was the one to provision volume %q: %v", volume.Name, err)
	}
	if !provisioned {
		strerr := fmt.Sprintf("this provisioner id %s didn't provision volume %q and so can't delete it; id %s did & can", p.identity, volume.Name, volume.Annotations[annProvisionerID])
		return &controller.IgnoredError{Reason: strerr}
	}

	err = p.deleteDirectory(volume)
	if err != nil {
		return fmt.Errorf("error deleting volume's backing path: %v", err)
	}

	err = p.deleteExport(volume)
	if err != nil {
		return fmt.Errorf("deleted the volume's backing path but error deleting export: %v", err)
	}

	err = p.deleteQuota(volume)
	if err != nil {
		return fmt.Errorf("deleted the volume's backing path & export but error deleting quota: %v", err)
	}

	return nil
}

func (p *nfsProvisioner) provisioned(volume *v1.PersistentVolume) (bool, error) {
	provisionerID, ok := volume.Annotations[annProvisionerID]
	if !ok {
		return false, fmt.Errorf("PV doesn't have an annotation %s", annProvisionerID)
	}

	return provisionerID == string(p.identity), nil
}

func (p *nfsProvisioner) deleteDirectory(volume *v1.PersistentVolume) error {
	path := path.Join(p.exportDir, volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}

	return nil
}

func (p *nfsProvisioner) deleteExport(volume *v1.PersistentVolume) error {
	block, exportID, err := getBlockAndID(volume, annExportBlock, annExportID)
	if err != nil {
		return fmt.Errorf("error getting block &/or id from annotations: %v", err)
	}

	if err := p.exporter.RemoveExportBlock(block, uint16(exportID)); err != nil {
		return fmt.Errorf("error removing the export from the config file: %v", err)
	}

	if err := p.exporter.Unexport(volume); err != nil {
		return fmt.Errorf("removed export from the config file but error unexporting it: %v", err)
	}

	return nil
}

func (p *nfsProvisioner) deleteQuota(volume *v1.PersistentVolume) error {
	block, projectID, err := getBlockAndID(volume, annProjectBlock, annProjectID)
	if err != nil {
		return fmt.Errorf("error getting block &/or id from annotations: %v", err)
	}

	if err := p.quotaer.RemoveProject(block, uint16(projectID)); err != nil {
		return fmt.Errorf("error removing the quota project from the projects file: %v", err)
	}

	if err := p.quotaer.UnsetQuota(); err != nil {
		return fmt.Errorf("removed quota project from the project file but error unsetting the quota: %v", err)
	}

	return nil
}

func getBlockAndID(volume *v1.PersistentVolume, annBlock, annID string) (string, uint16, error) {
	block, ok := volume.Annotations[annBlock]
	if !ok {
		return "", 0, fmt.Errorf("PV doesn't have an annotation with key %s", annBlock)
	}

	idStr, ok := volume.Annotations[annID]
	if !ok {
		return "", 0, fmt.Errorf("PV doesn't have an annotation %s", annID)
	}
	id, _ := strconv.ParseUint(idStr, 10, 16)

	return block, uint16(id), nil
}
