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
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"k8s.io/api/core/v1"
)

type ShareBackend interface {
	// Name of the share backend
	Name() string

	// Called once the share is created. Should grant share-specific access rules.
	GrantAccess(*GrantAccessArgs) (*shares.AccessRight, error)

	// Called during share provision, the result is used in the final PersistentVolume object.
	CreateSource(*CreateSourceArgs) (*v1.PersistentVolumeSource, error)

	// Called during share deletion. Should release any resources acquired by this share backend.
	Release(*ReleaseArgs) error
}
