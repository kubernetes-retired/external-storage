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
	"github.com/gophercloud/gophercloud"
	volumes_v2 "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
)

// volumeServiceBroker provides a mechanism for tests to override calls to cinder with mocks.
type volumeServiceBroker interface {
	createCinderVolume(vs *gophercloud.ServiceClient, options volumes_v2.CreateOpts) (string, error)
	waitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	reserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	connectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (volumeservice.VolumeConnection, error)
	disconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	unreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	deleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	getCinderVolumeStatus(vs *gophercloud.ServiceClient, volumeID string) (string, error)
}

type gophercloudBroker struct {
	volumeServiceBroker
}

func (vsb *gophercloudBroker) createCinderVolume(vs *gophercloud.ServiceClient, options volumes_v2.CreateOpts) (string, error) {
	return volumeservice.CreateCinderVolume(vs, options)
}

func (vsb *gophercloudBroker) waitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.WaitForAvailableCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) reserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.ReserveCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) connectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (volumeservice.VolumeConnection, error) {
	return volumeservice.ConnectCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) disconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.DisconnectCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) unreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.UnreserveCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) deleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.DeleteCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) getCinderVolumeStatus(vs *gophercloud.ServiceClient, volumeID string) (string, error) {
	return volumeservice.GetCinderVolumeStatus(vs, volumeID)
}
