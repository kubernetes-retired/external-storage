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
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/manila/pkg/shareservice"
)

// manilaServiceBroker provides a mechanism for tests to override calls to manila with mocks.
type manilaServiceBroker interface {
	createManilaShare(ms *gophercloud.ServiceClient, config shareservice.ShareConfig, options controller.VolumeOptions) ([]shares.ExportLocation, string, error)
	deleteManilaShare(ms *gophercloud.ServiceClient, shareID string) error
}

type gophercloudBroker struct {
	manilaServiceBroker
}

func (vsb *gophercloudBroker) createManilaShare(ms *gophercloud.ServiceClient, config shareservice.ShareConfig, options controller.VolumeOptions) ([]shares.ExportLocation, string, error) {
	return shareservice.CreateManilaShare(ms, config, options)
}

func (vsb *gophercloudBroker) deleteManilaShare(ms *gophercloud.ServiceClient, shareID string) error {
	return shareservice.DeleteManilaShare(ms, shareID)
}
