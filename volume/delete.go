package volume

import (
	"fmt"
	"os"
	"os/exec"

	"k8s.io/client-go/1.4/pkg/api/v1"
)

// Delete removes the directory backing the given PV that was created by
// createVolume.
func Delete(volume *v1.PersistentVolume) error {
	// TODO quota, something better than just directories
	path := fmt.Sprintf("/export/%s", volume.ObjectMeta.Name)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("error deleting volume by removing its path: %v", err)
	}

	cmd := exec.Command("exportfs", "-u", "*:"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -u failed with error: %v, output: %s", err, out)
	}

	return nil
}

// Exists returns true if the directory backing the given PV exists and so can
// be deleted
func Exists(volume *v1.PersistentVolume) bool {
	path := fmt.Sprintf("/export/%s", volume.ObjectMeta.Name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return false
}
