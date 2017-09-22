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
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
)

type volumeServiceBroker interface {
	CreateCinderVolume(vs *gophercloud.ServiceClient, options controller.VolumeOptions) (string, error)
	WaitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	ReserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	ConnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (volumeservice.VolumeConnection, error)
	DisconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	UnreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
	DeleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error
}

type gophercloudBroker struct {
	volumeServiceBroker
}

func (vsb *gophercloudBroker) CreateCinderVolume(vs *gophercloud.ServiceClient, options controller.VolumeOptions) (string, error) {
	return volumeservice.CreateCinderVolume(vs, options)
}

func (vsb *gophercloudBroker) WaitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.WaitForAvailableCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) ReserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.ReserveCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) ConnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (volumeservice.VolumeConnection, error) {
	return volumeservice.ConnectCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) DisconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.DisconnectCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) UnreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.UnreserveCinderVolume(vs, volumeID)
}

func (vsb *gophercloudBroker) DeleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeservice.DeleteCinderVolume(vs, volumeID)
}
