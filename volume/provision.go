package volume

import (
	"fmt"
	"os"
	"os/exec"

	"k8s.io/client-go/1.4/pkg/api/v1"
)

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume
func Provision(options VolumeOptions) (*v1.PersistentVolume, error) {
	// instead of createVolume could call out a script of some kind
	server, path, err := createVolume(options)
	if err != nil {
		return nil, err
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:   options.PVName,
			Labels: map[string]string{},
			Annotations: map[string]string{
				"kubernetes.io/createdby": "nfs-dynamic-provisioner",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.Capacity,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

// createVolume creates a volume i.e. the storage asset. It creates a unique
// directory under /exports (which could be the mountpoint of some persistent
// storage or just the ephemeral container directory) and exports it.
func createVolume(options VolumeOptions) (string, string, error) {
	// TODO take and validate Parameters
	if options.Parameters != nil {
		return "", "", fmt.Errorf("Invalid parameter: no StorageClass parameters are supported")
	}

	// TODO implement options.ProvisionerSelector parsing
	// TODO pv.Labels MUST be set to match claim.spec.selector
	if options.Selector != nil {
		return "", "", fmt.Errorf("claim.Spec.Selector is not supported")
	}

	// TODO quota, something better than just directories
	// TODO figure out permissions: gid, chgrp, root_squash
	path := fmt.Sprintf("/exports/%s", options.PVName)
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", "", err
	}
	cmd := exec.Command("exportfs", "-o", "rw,no_root_squash,sync", "*:"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return "", "", fmt.Errorf("Export failed with error: %v, output: %s", err, out)
	}

	// TODO use a service somehow, not the pod IP
	out, err = exec.Command("hostname", "-i").Output()
	if err != nil {
		os.RemoveAll(path)
		return "", "", err
	}
	server := string(out)

	return server, path, nil
}
