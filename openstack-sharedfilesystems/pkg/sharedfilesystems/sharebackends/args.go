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

package sharebackends

import (
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/shareoptions"
	clientset "k8s.io/client-go/kubernetes"
)

// CreateSourceArgs contains arguments for ShareBackend.CreateSource()
type CreateSourceArgs struct {
	Share       *shares.Share
	Options     *shareoptions.ShareOptions
	Location    *shares.ExportLocation
	Clientset   clientset.Interface
	AccessRight *shares.AccessRight
}

// GrantAccessArgs contains arguments for ShareBackend.GrantAccess()
type GrantAccessArgs struct {
	Share  *shares.Share
	Client *gophercloud.ServiceClient
}

// ReleaseArgs contains arguments for ShareBaceknd.Release()
type ReleaseArgs struct {
	ShareID   string
	Clientset clientset.Interface
}
