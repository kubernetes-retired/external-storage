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

package v1

//VsmSpec holds the config for creating a VSM
type VsmSpec struct {
	Kind       string `yaml:"kind"`
	APIVersion string `yaml:"apiVersion"`
	Metadata   struct {
		Name   string `yaml:"name"`
		Labels struct {
			Storage string `yaml:"volumeprovisioner.mapi.openebs.io/storage-size"`
		}
	} `yaml:"metadata"`
}

// Volume is a command implementation struct
type Volume struct {
	Spec struct {
		AccessModes interface{} `json:"AccessModes"`
		Capacity    interface{} `json:"Capacity"`
		ClaimRef    interface{} `json:"ClaimRef"`
		OpenEBS     struct {
			VolumeID string `json:"volumeID"`
		} `json:"OpenEBS"`
		PersistentVolumeReclaimPolicy string `json:"PersistentVolumeReclaimPolicy"`
		StorageClassName              string `json:"StorageClassName"`
	} `json:"Spec"`

	Status struct {
		Message string `json:"Message"`
		Phase   string `json:"Phase"`
		Reason  string `json:"Reason"`
	} `json:"Status"`
	Metadata struct {
		Annotations       interface{} `json:"annotations"`
		CreationTimestamp interface{} `json:"creationTimestamp"`
		Name              string      `json:"name"`
	} `json:"metadata"`
}
