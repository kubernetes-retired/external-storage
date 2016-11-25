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
	"strconv"

	"k8s.io/client-go/pkg/api/v1"
)

// Delete removes the directory that was created by Provision backing the given
// PV.
func (p *nfsProvisioner) Delete(volume *v1.PersistentVolume) error {
	err := p.deleteDirectory(volume)
	if err != nil {
		return fmt.Errorf("error deleting volume's backing path: %v", err)
	}

	err = p.deleteExport(volume)
	if err != nil {
		return fmt.Errorf("deleted the volume's backing path but error deleting export: %v", err)
	}

	return nil
}

func (p *nfsProvisioner) deleteDirectory(volume *v1.PersistentVolume) error {
	path := fmt.Sprintf(p.exportDir+"%s", volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("Delete called on a volume that doesn't exist, presumably because this provisioner never created it")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("error deleting backing path: %v", err)
	}

	return nil
}

func (p *nfsProvisioner) deleteExport(volume *v1.PersistentVolume) error {
	exportIdStr, ok := volume.Annotations[annExportId]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the config file", annExportId)
	}
	exportId, _ := strconv.ParseUint(exportIdStr, 10, 16)
	block, ok := volume.Annotations[annBlock]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the config file", annBlock)
	}

	if err := p.exporter.RemoveExportBlock(block, uint16(exportId)); err != nil {
		return fmt.Errorf("error removing the export from the config file: %v", err)
	}

	err := p.exporter.Unexport(volume)
	if err != nil {
		return fmt.Errorf("removed export from the config file but error unexporting it: %v", err)
	}

	return nil
}
