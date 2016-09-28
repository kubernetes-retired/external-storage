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
