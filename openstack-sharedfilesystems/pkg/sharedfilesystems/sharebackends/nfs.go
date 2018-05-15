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

// NFS struct, implements ShareBackend interface for NFS
type NFS struct{}

// Name of the backend
func (NFS) Name() string { return "nfs" }

// CreateSource creates builds PersistentVolumeSource
func (NFS) CreateSource(args *CreateSourceArgs) (*v1.PersistentVolumeSource, error) {
	server, path, err := splitExportLocation(args.Location)
	if err != nil {
		return nil, err
	}

	return &v1.PersistentVolumeSource{
		NFS: &v1.NFSVolumeSource{
			Server:   server,
			Path:     path,
			ReadOnly: false,
		},
	}, nil
}

// Release does nothing - nothing to release
func (NFS) Release(*ReleaseArgs) error {
	return nil
}

// GrantAccess to NFS share. Allows read-write access to everyone!
// TODO: make this configurable by StorageClass?
func (NFS) GrantAccess(args *GrantAccessArgs) (*shares.AccessRight, error) {
	return shares.GrantAccess(args.Client, args.Share.ID, shares.GrantAccessOpts{
		AccessType:  "ip",
		AccessTo:    "0.0.0.0/0",
		AccessLevel: "rw",
	}).Extract()
}
