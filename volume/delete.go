/*
Copyright 2016 Red Hat, Inc.

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
	"os/exec"
	"strconv"

	"github.com/guelfey/go.dbus"
	"k8s.io/client-go/1.4/pkg/api/v1"
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
	if ann, ok := volume.Annotations[annExportId]; ok {
		// If PV doesn't have this annotation it's no big deal for knfs
		exportId, _ := strconv.ParseUint(ann, 10, 16)
		p.deleteExportId(uint16(exportId))
	}

	block, ok := volume.Annotations[annBlock]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the config file %s ", p.exporter.GetConfig(), annBlock)
	}
	if err := p.removeFromFile(p.exporter.GetConfig(), block); err != nil {
		return fmt.Errorf("error removing the export from the config file %s: %v", p.exporter.GetConfig(), err)
	}

	err := p.exporter.Unexport(volume)
	if err != nil {
		return fmt.Errorf("removed export from the config file %s but error unexporting it: %v", p.exporter.GetConfig(), err)
	}

	return nil
}

func (e *ganeshaExporter) Unexport(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[annExportId]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the server", annExportId)
	}
	exportId, _ := strconv.ParseUint(ann, 10, 16)

	// Call RemoveExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.RemoveExport", 0, uint16(exportId))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.RemoveExport: %v", call.Err)
	}

	return nil
}

func (e *kernelExporter) Unexport(volume *v1.PersistentVolume) error {
	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
}
