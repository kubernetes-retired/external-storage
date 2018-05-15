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

package sharedfilesystems

import (
	"fmt"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/sharebackends"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/shareoptions"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type ManilaProvisioner struct {
	client    *gophercloud.ServiceClient
	clientset *kubernetes.Clientset
}

func NewManilaProvisioner(client *gophercloud.ServiceClient, clientset *kubernetes.Clientset) *ManilaProvisioner {
	return &ManilaProvisioner{
		client:    client,
		clientset: clientset,
	}
}

func (p *ManilaProvisioner) Provision(volOptions controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if volOptions.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	// Initialization

	shareOptions, err := shareoptions.NewShareOptions(&volOptions)
	if err != nil {
		return nil, err
	}

	shareBackend, err := getShareBackend(shareOptions.Backend)
	if err != nil {
		return nil, err
	}

	// Share creation

	share, err := createShare(&volOptions, shareOptions, p.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create a share: %v", err)
	}

	defer func() {
		// Delete the share if any of its setup operations fail
		if err != nil {
			if delErr := deleteShare(share.ID, p.client, p.clientset); delErr != nil {
				glog.Errorf("failed to delete share %s in a rollback procedure: %v", share.ID, delErr)
			}
		}
	}()

	if err = waitForShareStatus(share.ID, p.client, "available"); err != nil {
		return nil, fmt.Errorf("waiting for share %s to become created failed: %v", share.ID, err)
	}

	availableExportLocations, err := shares.GetExportLocations(p.client, share.ID).Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to get export locations for share %s: %v", share.ID, err)
	}

	chosenExportLocation, err := chooseExportLocation(availableExportLocations)
	if err != nil {
		return nil, fmt.Errorf("failed to choose an export location for share %s: %v", share.ID, err)
	}

	accessRight, err := shareBackend.GrantAccess(&sharebackends.GrantAccessArgs{Share: share, Client: p.client})
	if err != nil {
		return nil, fmt.Errorf("failed to grant access for share %s: %v", share.ID, err)
	}

	volSource, err := shareBackend.CreateSource(&sharebackends.CreateSourceArgs{
		Share:       share,
		Options:     shareOptions,
		Location:    &chosenExportLocation,
		Clientset:   p.clientset,
		AccessRight: accessRight,
	})
	if err != nil {
		return nil, fmt.Errorf("backend %s failed to create volume source for share %s: %v", shareBackend.Name(), share.ID, err)
	}

	// For deleteShare()
	registerBackendForShare(shareOptions.Backend, share.ID)

	return buildPersistentVolume(share, &volOptions, volSource), nil
}

func (p *ManilaProvisioner) Delete(volume *v1.PersistentVolume) error {
	shareId, err := getShareIDfromPV(volume)
	if err != nil {
		return err
	}

	if err = deleteShare(shareId, p.client, p.clientset); err != nil {
		return fmt.Errorf("failed to delete share %s: %v", shareId, err)
	}

	return nil
}
