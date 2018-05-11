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
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"k8s.io/api/core/v1"
)

type NFS struct{}

func (NFS) Name() string { return "nfs" }

func (NFS) CreateSource(args *CreateSourceArgs) (*v1.PersistentVolumeSource, error) {
	split := strings.SplitN(args.Location.Path, ":", 2)
	if len(split) != 2 {
		return nil, fmt.Errorf("failed to parse server and path from location '%s'", args.Location.Path)
	}

	return &v1.PersistentVolumeSource{
		NFS: &v1.NFSVolumeSource{
			Server:   split[0],
			Path:     split[1],
			ReadOnly: false,
		},
	}, nil
}

func (NFS) Release(*ReleaseArgs) error {
	return nil
}

func (NFS) GrantAccess(args *GrantAccessArgs) (*shares.AccessRight, error) {
	return shares.GrantAccess(args.Client, args.Share.ID, shares.GrantAccessOpts{
		AccessType:  "ip",
		AccessTo:    "0.0.0.0/0",
		AccessLevel: "rw",
	}).Extract()
}
