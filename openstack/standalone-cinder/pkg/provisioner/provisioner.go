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
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// ProvisionerName is the unique name of this provisioner
	ProvisionerName = "openstack.org/standalone-cinder"

	// ProvisionerIDAnn is an annotation to identify a particular instance of this provisioner
	ProvisionerIDAnn = "standaloneCinderProvisionerIdentity"

	// CinderVolumeID is an annotation to store the ID of the associated cinder volume
	CinderVolumeID = "cinderVolumeId"
)

type cinderProvisioner struct {
	// Openstack cinder client
	VolumeService *gophercloud.ServiceClient

	// Kubernetes Client. Use to create secret
	Client kubernetes.Interface
	// Identity of this cinderProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	Identity string
}

// NewCinderProvisioner returns a Provisioner that creates volumes using a
// standalone cinder instance and produces PersistentVolumes that use native
// kubernetes PersistentVolumeSources.
func NewCinderProvisioner(client kubernetes.Interface, id, configFilePath string) (controller.Provisioner, error) {
	volumeService, err := volumeservice.GetVolumeService(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume service: %v", err)
	}

	return &cinderProvisioner{
		VolumeService: volumeService,
		Client:        client,
		Identity:      id,
	}, nil
}

type provisionCtx struct {
	P          *cinderProvisioner
	Options    controller.VolumeOptions
	Connection volumeservice.VolumeConnection
}

type deleteCtx struct {
	P  *cinderProvisioner
	PV *v1.PersistentVolume
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *cinderProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	volumeID, err := volumeservice.CreateCinderVolume(p.VolumeService, options)
	if err != nil {
		glog.Errorf("Failed to create volume")
		return nil, err
	}

	err = volumeservice.WaitForAvailableCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Volume did not become available")
		return nil, err
	}

	err = volumeservice.ReserveCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to reserve volume: %v", err)
		return nil, err
	}

	connection, err := volumeservice.ConnectCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to connect volume: %v", err)
		return nil, err
	}

	mapper, err := newVolumeMapperFromConnection(connection)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Unable to create volume mapper: %f", err)
		return nil, err
	}

	ctx := provisionCtx{p, options, connection}
	err = mapper.AuthSetup(ctx)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to prepare volume auth: %v", err)
		return nil, err
	}

	pv, err := buildPV(mapper, ctx, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to build PV: %v", err)
		return nil, err
	}

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *cinderProvisioner) Delete(pv *v1.PersistentVolume) error {
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

	volumeID, ok := pv.Annotations[CinderVolumeID]
	if !ok {
		return errors.New(CinderVolumeID + " annotation not found on PV")
	}

	ctx := deleteCtx{p, pv}
	mapper, err := newVolumeMapperFromPV(ctx)
	if err != nil {
		return err
	}

	mapper.AuthTeardown(ctx)

	err = volumeservice.DisconnectCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		return err
	}

	err = volumeservice.UnreserveCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to unreserve volume: %v", err)
		return err
	}

	err = volumeservice.DeleteCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		return err
	}

	glog.V(2).Infof("Successfully deleted cinder volume %s", volumeID)
	return nil
}
