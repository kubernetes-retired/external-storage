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

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
)

func (p *flexProvisioner) Delete(volume *v1.PersistentVolume) error {
	glog.Infof("Delete called for volume: %s", volume.Name)

	provisioned, err := p.provisioned(volume)
	if err != nil {
		return fmt.Errorf("error determining if this provisioner was the one to provision volume %q: %v", volume.Name, err)
	}
	if !provisioned {
		strerr := fmt.Sprintf("this provisioner id %s didn't provision volume %q and so can't delete it; id %s did & can", p.identity, volume.Name, volume.Annotations[annProvisionerID])
		return &controller.IgnoredError{Reason: strerr}
	}

	extraOptions := map[string]string{}
	extraOptions[optionPVorVolumeName] = volume.Name

	call := p.NewDriverCall(p.execCommand, deleteCmd)
	call.AppendSpec(volume.Spec.FlexVolume.Options, extraOptions)
	output, err := call.Run()

	if err != nil {
		glog.Errorf("Failed to delete volume %s, output: %s, error: %s", volume, output.Message, err.Error())
		return err
	}
	return nil
}

func (p *flexProvisioner) provisioned(volume *v1.PersistentVolume) (bool, error) {
	provisionerID, ok := volume.Annotations[annProvisionerID]
	if !ok {
		return false, fmt.Errorf("PV doesn't have an annotation %s", annProvisionerID)
	}

	return provisionerID == string(p.identity), nil
}
