// Copyright 2016 Red Hat, Inc.

// 	Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// 	You may obtain a copy of the License at

// http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// 	See the License for the specific language governing permissions and
// limitations under the License.

package volume

import (
	"fmt"
	"os"
	"strconv"

	"github.com/guelfey/go.dbus"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// Delete removes the directory backing the given PV that was created by
// createVolume.
func Delete(volume *v1.PersistentVolume) error {
	// TODO quota, something better than just directories

	path := fmt.Sprintf(exportDir+"%s", volume.ObjectMeta.Name)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("error deleting volume by removing its path: %v", err)
	}

	ann, ok := volume.Annotations[annExportId]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the server even though the exported directory is gone", annExportId)
	}
	exportId, _ := strconv.Atoi(ann)

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

	block, ok := volume.Annotations[annBlock]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, removed the exported directory and the export from the server but can't remove the export from the config file", annBlock)
	}
	removeExportBlock(block)

	return nil
}

// Exists returns true if the directory backing the given PV exists and so can
// be deleted
func Exists(volume *v1.PersistentVolume) bool {
	path := fmt.Sprintf(exportDir+"%s", volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}
