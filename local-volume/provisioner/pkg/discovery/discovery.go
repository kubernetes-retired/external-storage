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
	"hash/fnv"
	"path/filepath"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/types"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
)

// Discoverer finds available volumes and creates PVs for them
// It looks for volumes in the directories specified in the discoveryMap
type Discoverer struct {
	*types.RuntimeConfig
	nodeAffinity *v1.NodeAffinity
}

func NewDiscoverer(config *types.RuntimeConfig) (*Discoverer, error) {
	affinity, err := generateNodeAffinity(config.Node)
	if err != nil {
		return nil, err
	}
	return &Discoverer{RuntimeConfig: config, nodeAffinity: affinity}, nil
}

func generateNodeAffinity(node *v1.Node) (*v1.NodeAffinity, error) {
	if node.Labels == nil {
		return nil, fmt.Errorf("Node does not have labels")
	}
	nodeValue, found := node.Labels[types.NodeLabelKey]
	if !found {
		return nil, fmt.Errorf("Node does not have expected label %s", types.NodeLabelKey)
	}

	return &v1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
			NodeSelectorTerms: []v1.NodeSelectorTerm{
				{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      types.NodeLabelKey,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{nodeValue},
						},
					},
				},
			},
		},
	}, nil
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
		glog.Errorf("Error reading directory: %v", err)
		return
	}

	for _, file := range files {
		// Check if PV already exists for it
		pvName := generatePVName(file, d.Node.Name, class)
		if !d.Cache.PVExists(pvName) {
			filePath := filepath.Join(fullPath, file)
			err = d.validateFile(filePath)
			if err != nil {
				glog.Errorf("Path %q validation failed: %v", filePath, err)
				continue
			}
			// TODO: detect capacity
			d.createPV(file, relativePath, class)
		}
	}
}

func (d *Discoverer) validateFile(fullPath string) error {
	isDir, err := d.VolUtil.IsDir(fullPath)
	if err != nil {
		return fmt.Errorf("Error getting path info: %v", err)
	}
	if !isDir {
		return fmt.Errorf("Path is not a directory")
	}
	return nil
}

func generatePVName(file, node, class string) string {
	h := fnv.New32a()
	h.Write([]byte(file))
	h.Write([]byte(node))
	h.Write([]byte(class))
	// This is the FNV-1a 32-bit hash
	return fmt.Sprintf("local-pv-%x", h.Sum32())
}

func (d *Discoverer) createPV(file, relativePath, class string) {
	pvName := generatePVName(file, d.Node.Name, class)
	outsidePath := filepath.Join(d.HostDir, relativePath, file)

	glog.Infof("Found new volume at host path %q, creating Local PV %q", outsidePath, pvName)
	pvSpec := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Annotations: map[string]string{
				types.AnnProvisionedBy: d.Name,
				// TODO: add topology constraint once we have API
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
			Capacity: v1.ResourceList{
				// TODO: detect capacity
				v1.ResourceName(v1.ResourceStorage): resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
			// TODO update when we have API
			/**
			Local: &v1.LocalVolumeSource{
				Path: outsidePath,
			},
			*/
			},
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			StorageClassName: class,
		},
	}

	pv, err := d.APIUtil.CreatePV(pvSpec)
	if err != nil {
		glog.Errorf("Error creating PV %q for volume at %q: %v", pvName, outsidePath, err)
		return
	}
	d.Cache.AddPV(pv)
	glog.Infof("Created PV %q for volume at %q", pvName, outsidePath)
}
