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

package discovery

import (
	"fmt"
	"path/filepath"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/types"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
)

type Discoverer struct {
	*types.RuntimeConfig
}

func NewDiscoverer(config *types.RuntimeConfig) *Discoverer {
	return &Discoverer{RuntimeConfig: config}
}

// DiscoverLocalVolumes reads the configured discovery paths, and creates PVs for the new volumes
func (d *Discoverer) DiscoverLocalVolumes() {
	for class, path := range d.DiscoveryMap {
		d.discoverVolumesAtPath(class, path)
	}
}

func (d *Discoverer) discoverVolumesAtPath(class, relativePath string) {
	fullPath := filepath.Join(d.MountDir, relativePath)
	glog.Infof("Discovering volumes at mount path %q for storage class %q", fullPath, class)

	files, err := d.VolUtil.ReadDir(fullPath)
	if err != nil {
		glog.Errorf("Error reading directory: %v\n", err)
		return
	}

	for _, file := range files {
		// Check if PV already exists for it
		pvName := generatePVName(file, d.NodeName, class)
		if !d.Cache.PVExists(pvName) {
			// If not, create PV
			// TODO: Validate that this path is file-based, and detect capacity
			d.createPV(file, relativePath, class)
		}
	}
}

// TODO: maybe a better way would be to hash the 3 fields
func generatePVName(file, node, class string) string {
	return fmt.Sprintf("%v-%v-%v", class, node, file)
}

func (d *Discoverer) createPV(file, relativePath, class string) {
	pvName := generatePVName(file, d.NodeName, class)
	outsidePath := filepath.Join(d.HostDir, relativePath, file)

	glog.Infof("Found new volume at host path %v, creating LocalVolume PV %q\n", outsidePath, pvName)
	pvSpec := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Annotations: map[string]string{
				common.AnnProvisionedBy: d.Name,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
			Capacity: v1.ResourceList{
				// TODO: detect capacity
				v1.ResourceName(v1.ResourceStorage): resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
			/** TODO: api not merged yet
			LocalVolume: &v1.LocalVolumeSource{
				NodeName: config.NodeName,
				Fs: v1.LocalFsVolume{Path: outsidePath},
			},*/
			},
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			StorageClassName: "local-fast",
		},
	}

	pv, err := d.APIUtil.CreatePV(pvSpec)
	if err != nil {
		glog.Errorf("Error creating PV %q: %v", pvName, err)
		return
	}
	err = d.Cache.AddPV(pv)
	if err != nil {
		glog.Errorf("Error adding PV %q to cache: %v", pvName, err)
		return
	}
	glog.Infof("Created PV: %v", pvName)
}
