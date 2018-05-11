/*
Copyright 2018 The Kubernetes Authors.

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

package shareoptions

import (
	"fmt"

	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"
)

type ShareOptions struct {
	ShareName string

	CommonOptions   // Required common options
	ProtocolOptions // Protocol specific options
	BackendOptions  // Backend specific options
}

func NewShareOptions(volOptions *controller.VolumeOptions) (*ShareOptions, error) {
	params := volOptions.Parameters
	opts := &ShareOptions{}
	nParams := len(params)

	opts.ShareName = "pvc-" + string(volOptions.PVC.GetUID())

	// Set default values

	setDefaultValue("type", params, "default")
	setDefaultValue("zones", params, "nova")

	// Required common options
	if n, err := extractParams(&optionConstraints{}, params, &opts.CommonOptions); err != nil {
		return nil, err
	} else {
		nParams -= n
	}

	constraints := optionConstraints{protocol: opts.Protocol, backend: opts.Backend}

	// Protocol specific options
	if n, err := extractParams(&constraints, params, &opts.ProtocolOptions); err != nil {
		return nil, err
	} else {
		nParams -= n
	}

	// Backend specific options
	if n, err := extractParams(&constraints, params, &opts.BackendOptions); err != nil {
		return nil, err
	} else {
		nParams -= n
	}

	if nParams != 0 {
		return nil, fmt.Errorf("parameters contain invalid field(s)")
	}

	if setOfZones, err := util.ZonesToSet(opts.Zones); err != nil {
		return nil, err
	} else {
		opts.Zones = volume.ChooseZoneForVolume(setOfZones, volOptions.PVC.GetName())
	}

	return opts, nil
}
