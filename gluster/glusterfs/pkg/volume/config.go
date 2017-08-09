/*
Copyright 2016 The Kubernetes Authors.

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
	"strings"
)

// BrickRootPath is root path of brick for each Gluster Host
type BrickRootPath struct {
	Host string
	Path string
}

// ProvisionerConfig provisioner config for Provision Volume
type ProvisionerConfig struct {
	ForceCreate    bool
	Namespace      string
	LabelSelector  string
	BrickRootPaths []BrickRootPath
	VolumeName     string
	VolumeType     string
}

// NewProvisionerConfig create ProvisionerConfig from parameters of StorageClass
func NewProvisionerConfig(pvName string, params map[string]string) (*ProvisionerConfig, error) {
	var config ProvisionerConfig
	var err error

	// Set default volume type
	forceCreate := false
	volumeType := ""
	namespace := "default"
	selector := "glusterfs-node==pod"
	var brickRootPaths []BrickRootPath

	for k, v := range params {
		switch strings.ToLower(k) {
		case "brickrootpaths":
			brickRootPaths, err = parseBrickRootPaths(v)
			if err != nil {
				return nil, err
			}
		case "volumetype":
			volumeType = strings.TrimSpace(v)
		case "namespace":
			namespace = strings.TrimSpace(v)
		case "selector":
			selector = strings.TrimSpace(v)
		case "forcecreate":
			v = strings.TrimSpace(v)
			forceCreate = strings.ToLower(v) == "true"
		}
	}

	config.BrickRootPaths = brickRootPaths
	config.VolumeName = pvName
	config.VolumeType = volumeType
	config.Namespace = namespace
	config.LabelSelector = selector
	config.ForceCreate = forceCreate

	err = config.validate()
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func parseBrickRootPaths(param string) ([]BrickRootPath, error) {
	pairs := strings.Split(param, ",")
	brickRootPaths := make([]BrickRootPath, len(pairs))
	for i, path := range pairs {
		path = strings.TrimSpace(path)
		rawBrickPath := strings.Split(path, ":")
		if len(rawBrickPath) < 2 {
			return nil, fmt.Errorf("BrickRootPath is invalid (format is `host:/path/to/root,host2:/path/to/root2`): %s", param)
		}
		brickRootPaths[i].Host = rawBrickPath[0]
		brickRootPaths[i].Path = rawBrickPath[1]
	}

	return brickRootPaths, nil
}

func (config *ProvisionerConfig) validate() error {
	if len(config.BrickRootPaths) == 0 {
		return fmt.Errorf("brickRootPaths are not specified")
	}

	return nil
}
