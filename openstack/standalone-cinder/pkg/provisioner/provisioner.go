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

	vsb volumeServiceBroker
	mb  mapperBroker
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
		vsb:           &gophercloudBroker{},
		mb:            &volumeMapperBroker{},
	}, nil
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *cinderProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	var (
		connection volumeservice.VolumeConnection
		mapper     volumeMapper
		pv         *v1.PersistentVolume
		cleanupErr error
	)

	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	// TODO: Check access mode

	volumeID, err := p.vsb.createCinderVolume(p.VolumeService, options)
	if err != nil {
		glog.Errorf("Failed to create volume")
		goto ERROR
	}

	err = p.vsb.waitForAvailableCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Volume %s did not become available", volumeID)
		goto ERROR_DELETE
	}

	err = p.vsb.reserveCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Failed to reserve volume %s: %v", volumeID, err)
		goto ERROR_DELETE
	}

	connection, err = p.vsb.connectCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Failed to connect volume %s: %v", volumeID, err)
		goto ERROR_UNRESERVE
	}

	mapper, err = p.mb.newVolumeMapperFromConnection(connection)
	if err != nil {
		glog.Errorf("Unable to create volume mapper: %v", err)
		goto ERROR_DISCONNECT
	}

	err = mapper.AuthSetup(p, options, connection)
	if err != nil {
		glog.Errorf("Failed to prepare volume auth: %v", err)
		goto ERROR_DISCONNECT
	}

	pv, err = p.mb.buildPV(mapper, p, options, connection, volumeID)
	if err != nil {
		glog.Errorf("Failed to build PV: %v", err)
		goto ERROR_DISCONNECT
	}
	return pv, nil

ERROR_DISCONNECT:
	cleanupErr = p.vsb.disconnectCinderVolume(p.VolumeService, volumeID)
	if cleanupErr != nil {
		glog.Errorf("Failed to disconnect volume %s: %v", volumeID, cleanupErr)
	}
	glog.V(3).Infof("Volume %s disconnected", volumeID)
ERROR_UNRESERVE:
	cleanupErr = p.vsb.unreserveCinderVolume(p.VolumeService, volumeID)
	if cleanupErr != nil {
		glog.Errorf("Failed to unreserve volume %s: %v", volumeID, cleanupErr)
	}
	glog.V(3).Infof("Volume %s unreserved", volumeID)
ERROR_DELETE:
	cleanupErr = p.vsb.deleteCinderVolume(p.VolumeService, volumeID)
	if cleanupErr != nil {
		glog.Errorf("Failed to delete volume %s: %v", volumeID, cleanupErr)
	}
	glog.V(3).Infof("Volume %s deleted", volumeID)
ERROR:
	return nil, err // Return the original error
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

	mapper, err := p.mb.newVolumeMapperFromPV(pv)
	if err != nil {
		glog.Errorf("Failed to instantiate mapper: %s", err)
		return err
	}

	mapper.AuthTeardown(p, pv)

	err = p.vsb.disconnectCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Failed to disconnect volume %s: %v", volumeID, err)
		return err
	}

	err = p.vsb.unreserveCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to unreserve volume %s: %v", volumeID, err)
		return err
	}

	err = p.vsb.deleteCinderVolume(p.VolumeService, volumeID)
	if err != nil {
		glog.Errorf("Failed to delete volume %s: %v", volumeID, err)
		return err
	}

	glog.V(2).Infof("Successfully deleted cinder volume %s", volumeID)
	return nil
}
