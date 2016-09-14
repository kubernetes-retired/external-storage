package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/golang/glog"

	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// Export is a share for the server to export and create a PV for
type Export struct {
	Path     string `json:"path"`
	Capacity string `json:"capacity"`
}

// what if there are errors partway through?
func provisionStatic(client kubernetes.Interface, configFile string) error {
	exports, err := loadValidConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load valid config from file %s: %v", configFile, err)
	}

	options := VolumeOptions{
		AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimRetain,
	}

	volumes, err := provision(options, exports)
	if err != nil {
		return fmt.Errorf("failed to provision PersistentVolumes: %v", err)
	}

	// Try to create the PV object several times
	for _, volume := range volumes {
		for i := 0; i < createProvisionedPVRetryCount; i++ {
			if _, err = client.Core().PersistentVolumes().Create(volume); err == nil {
				// Save succeeded.
				glog.V(3).Infof("volume %q saved", volume.Name)
				break
			}
			// Save failed, try again after a while.
			glog.V(3).Infof("failed to save volume %q: %v", volume.Name, err)
			time.Sleep(createProvisionedPVInterval)
		}
		// if err != nil {}
		// Save failed. Now we have a storage asset outside of Kubernetes,
		// ....
	}

	return nil
}

// loadValidConfig loads the json config file and validates its contents.
func loadValidConfig(configFile string) ([]Export, error) {
	file, err := ioutil.ReadFile(configFile)
	if err != nil {
		return []Export{}, fmt.Errorf("read config file %s failed: %v", configFile, err)
	}

	var config []Export
	err = json.Unmarshal(file, &config)
	if err != nil {
		return []Export{}, fmt.Errorf("unmarshal json config file %s failed: %v", configFile, err)
	}

	for _, export := range config {
		if _, err := os.Stat(export.Path); err != nil {
			return []Export{}, fmt.Errorf("stat path %s failed: %v", export.Path, err)
		}
		if _, err := resource.ParseQuantity(export.Capacity); err != nil {
			return []Export{}, fmt.Errorf("parse quantity %v failed: %v", export.Capacity, err)
		}
	}

	return config, nil
}

func provision(options VolumeOptions, exports []Export) ([]*v1.PersistentVolume, error) {
	server, err := createVolumes(exports)
	if err != nil {
		return nil, fmt.Errorf("create volumes failed: %v", err)
	}

	volumes := make([]*v1.PersistentVolume, len(exports))
	for _, export := range exports {
		pv := &v1.PersistentVolume{
			ObjectMeta: v1.ObjectMeta{
				Name:   strings.Replace(export.Path, "/", ".", -1),
				Labels: map[string]string{},
				Annotations: map[string]string{
					"kubernetes.io/createdby": "nfs-static-provisioner",
				},
			},
			Spec: v1.PersistentVolumeSpec{
				PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
				AccessModes:                   options.AccessModes,
				Capacity: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(export.Capacity),
				},
				PersistentVolumeSource: v1.PersistentVolumeSource{
					NFS: &v1.NFSVolumeSource{
						Server:   server,
						Path:     export.Path,
						ReadOnly: false,
					},
				},
			},
		}
		volumes = append(volumes, pv)
	}

	return volumes, nil
}

func createVolumes(exports []Export) (string, error) {
	err := populateExports(exports)
	if err != nil {
		return "", fmt.Errorf("populate /etc/exports failed: %v", err)
	}
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("'exportfs -r' failed with error: %v, output: %v", err, out)
	}

	out, err = exec.Command("hostname", "-i").Output()
	if err != nil {
		return "", err
	}
	server := string(out)

	return server, nil
}

func populateExports(exports []Export) error {
	f, err := os.OpenFile("/etc/exports", os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open /etc/exports file failed: %v", err)
	}
	defer f.Close()
	for _, export := range exports {
		if _, err = f.WriteString(export.Path + " *(rw,insecure,no_root_squash)\n"); err != nil {
			return fmt.Errorf("write to /etc/exports failed: %v", err)
		}
	}

	return nil
}
