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
	// TODO quota, something better than just directories

	if !p.Exists(volume) {
		return fmt.Errorf("Delete called on a volume that doesn't exist, presumably because this provisioner never created it")
	}

	path := fmt.Sprintf(p.exportDir+"%s", volume.ObjectMeta.Name)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("error deleting volume by removing its path: %v", err)
	}

	var err error
	if p.useGanesha {
		err = p.ganeshaUnexport(volume)
	} else {
		err = p.kernelUnexport(volume)
	}
	if err != nil {
		return fmt.Errorf("removed the path backing the volume but error unexporting it: %v", err)
	}

	return nil
}

func (p *nfsProvisioner) ganeshaUnexport(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[annExportId]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the server", annExportId)
	}
	exportId, _ := strconv.ParseUint(ann, 10, 16)
	delete(p.exportIds, uint16(exportId))

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

	// Error removing the EXPORT block from file is not really an error, ganesha
	// will just not export it next time around because the dir doesn't exist
	block, ok := volume.Annotations[annBlock]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, removed the export from the server but can't remove the export from the config file", annBlock)
	}
	if err := p.removeFromFile(p.ganeshaConfig, block); err != nil {
		return fmt.Errorf("removed the export from the server but error removing the export from the config file: %v", err)
	}

	return nil

}

func (p *nfsProvisioner) kernelUnexport(volume *v1.PersistentVolume) error {
	if ann, ok := volume.Annotations[annExportId]; ok {
		// If PV doesn't have this annotation it's no big deal for knfs
		exportId, _ := strconv.ParseUint(ann, 10, 16)
		delete(p.exportIds, uint16(exportId))
	}

	line, ok := volume.Annotations[annLine]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from /etc/exports", annLine)
	}
	if err := p.removeFromFile("/etc/exports", line); err != nil {
		return fmt.Errorf("error removing the export from /etc/exports: %v", err)
	}

	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
}

// Exists returns true if the directory backing the given PV exists and so can
// be deleted. Since multiple NFS provisioners can be running, we can't assume
// that the underlying volume was created by *this* one. This is a convenience
// function to call before calling Delete; Delete will still fail if this isn't
// true but presumably one wants to fail earlier than that.
func (p *nfsProvisioner) Exists(volume *v1.PersistentVolume) bool {
	path := fmt.Sprintf(p.exportDir+"%s", volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}
