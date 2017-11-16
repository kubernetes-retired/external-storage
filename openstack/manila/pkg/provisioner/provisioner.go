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

package provisioner

import (
	"errors"
	"fmt"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/manila/pkg/shareservice"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"strings"
)

const (
	// ProvisionerName is the unique name of this provisioner
	ProvisionerName = "openstack.org/manila"

	// ProvisionerIDAnn is an annotation to identify a particular instance of this provisioner
	ProvisionerIDAnn = "manilaProvisionerIdentity"

	// ManilaShareID is an annotation to store the ID of the associated manila share
	ManilaShareID = "manilaShareId"
)

type manilaProvisioner struct {
	// Openstack manila client
	ShareService *gophercloud.ServiceClient
	ShareConfig  shareservice.ShareConfig

	// Kubernetes Client. Use to create secret
	Client kubernetes.Interface
	// Identity of this manilaProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	Identity string

	msb manilaServiceBroker
}

// NewManilaProvisioner returns a Provisioner that creates shares calling
// openstack manila api and produces PersistentVolumes that use native
// kubernetes PersistentVolumeSources.
func NewManilaProvisioner(client kubernetes.Interface, id, configFilePath string) (controller.Provisioner, error) {
	shareService, err := shareservice.GetShareService(configFilePath)
	shareConfig, err := shareservice.GetShareConfig(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get share service: %v", err)
	}

	return &manilaProvisioner{
		ShareService: shareService,
		ShareConfig:  shareConfig,
		Client:       client,
		Identity:     id,
		msb:          &gophercloudBroker{},
	}, nil
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *manilaProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {

	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	// TODO: Check access mode

	shareLocations, shareID, err := p.msb.createManilaShare(p.ShareService, p.ShareConfig, options)
	location := strings.Split(shareLocations[0].Path, ":")
	if err != nil {
		glog.Errorf("Failed to create share")
		return nil, err // Return the original error
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				ProvisionerIDAnn: p.Identity,
				ManilaShareID:    shareID,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   location[0],
					Path:     location[1],
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil

}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *manilaProvisioner) Delete(pv *v1.PersistentVolume) error {
	ann, ok := pv.Annotations[ProvisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.Identity {
		return &controller.IgnoredError{
			Reason: "identity annotation on PV does not match ours",
		}
	}
	// TODO when beta is removed, have to check kube version and pick v1/beta
	// accordingly: maybe the controller lib should offer a function for that

	shareID, ok := pv.Annotations[ManilaShareID]
	if !ok {
		return errors.New(ManilaShareID + " annotation not found on PV")
	}

	err := p.msb.deleteManilaShare(p.ShareService, shareID)
	if err != nil {
		glog.Errorf("Failed to delete volume %s: %v", shareID, err)
		return err
	}

	glog.V(2).Infof("Successfully deleted cinder volume %s", shareID)
	return nil
}
