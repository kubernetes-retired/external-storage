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

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"k8s.io/api/core/v1"
)

type CSICephFS struct{}

func (CSICephFS) Name() string { return "csi-cephfs" }

func (CSICephFS) CreateSource(args *CreateSourceArgs) (*v1.PersistentVolumeSource, error) {
	monitors, rootPath, err := splitExportLocation(args.Location)
	if err != nil {
		return nil, err
	}

	sec := v1.Secret{
		Data: map[string][]byte{
			"userID":  []byte(args.AccessRight.AccessTo),
			"userKey": []byte(args.AccessRight.AccessKey),
		},
	}
	sec.Name = getSecretName(args.Share.ID)

	secResp, err := args.Clientset.CoreV1().Secrets("default").Create(&sec)
	if err != nil {
		return nil, fmt.Errorf("failed to create a secret: %v", err)
	}

	return &v1.PersistentVolumeSource{
		CSI: &v1.CSIPersistentVolumeSource{
			Driver:       args.Options.CSICEPHFS_driver,
			ReadOnly:     false,
			VolumeHandle: args.Options.ShareName,
			VolumeAttributes: map[string]string{
				"monitors":        monitors,
				"rootPath":        rootPath,
				"mounter":         "fuse",
				"provisionVolume": "false",
			},
			NodePublishSecretRef: &v1.SecretReference{
				Name:      secResp.GetName(),
				Namespace: secResp.GetNamespace(),
			},
		},
	}, nil
}

func (CSICephFS) Release(args *ReleaseArgs) error {
	return args.Clientset.CoreV1().Secrets("default").Delete(getSecretName(args.ShareID), nil)
}

func (CSICephFS) GrantAccess(args *GrantAccessArgs) (*shares.AccessRight, error) {
	accessOpts := shares.GrantAccessOpts{
		AccessType:  "cephx",
		AccessTo:    args.Share.Name,
		AccessLevel: "rw",
	}

	if _, err := shares.GrantAccess(args.Client, args.Share.ID, accessOpts).Extract(); err != nil {
		return nil, err
	}

	var accessRight shares.AccessRight

	err := gophercloud.WaitFor(120, func() (bool, error) {
		accessRights, err := shares.ListAccessRights(args.Client, args.Share.ID).Extract()
		if err != nil {
			return false, err
		}

		if len(accessRights) > 1 {
			return false, fmt.Errorf("unexpected number of access rules: got %d, expected 1", len(accessRights))
		} else if len(accessRights) == 0 {
			return false, nil
		}

		if accessRights[0].AccessKey != "" {
			accessRight = accessRights[0]
			return true, nil
		}

		return false, nil
	})

	return &accessRight, err
}
