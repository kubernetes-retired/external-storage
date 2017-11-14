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

package shareservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

// CreateManilaShare creates a new share in manila according to the PVC specifications
func CreateManilaShare(ms *gophercloud.ServiceClient, config ShareConfig, options controller.VolumeOptions) ([]shares.ExportLocation, string, error) {
	name := fmt.Sprintf("manila-dynamic-pvc-%s", uuid.NewUUID())
	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	sizeBytes := capacity.Value()
	// Manila works with gigabytes, convert to GiB with rounding up
	sizeGB := int((sizeBytes + 1024*1024*1024 - 1) / (1024 * 1024 * 1024))
	opts := &shares.CreateOpts{
		Name:           name,
		Size:           sizeGB,
		ShareProto:     config.ShareProtocol,
		ShareNetworkID: config.ShareNetworkID,
		ShareType:      config.ShareType,
	}

	share, err := shares.Create(ms, opts).Extract()
	if err != nil {
		glog.Errorf("Failed to create a %d GiB volume: %v", sizeGB, err)
		return nil, "", err
	}
	err = waitForAvailableManilaShare(ms, share.ID)
	if err != nil {
		glog.Errorf("Failed to create a %d GiB volume: %v", sizeGB, err)
		return nil, "", err
	}
	// Client c must have Microversion set; minimum supported microversion for Get Export Locations is 2.14
	// TODO: fix the hardcode here
	ms.Microversion = "2.14"
	shareLocation, err := shares.GetExportLocations(ms, share.ID).Extract()
	glog.V(2).Infof("Created share %v in Availability Zone: %v", share.ID, share.AvailabilityZone)

	grantAccessReq := shares.GrantAccessOpts{
		AccessType:  "ip",
		AccessTo:    "0.0.0.0/0",
		AccessLevel: "rw",
	}
	_, err = shares.GrantAccess(ms, share.ID, grantAccessReq).Extract()
	if err != nil {
		glog.Errorf("Failed to grant access to %v", share.ID)
		return nil, "", err
	}
	return shareLocation, share.ID, nil
}

// WaitForAvailableManilaShare waits for a newly created share to
// become available.
func waitForAvailableManilaShare(ms *gophercloud.ServiceClient, shareID string) error {
	c := make(chan error)
	go func() error {
		num := 0
		for {
			time.Sleep(3 * time.Second)
			if num > 10 {
				err := errors.New("Failed to get share after max retries")
				c <- err
			}
			fmt.Printf("Check share: %v status\n", shareID)
			share, err := shares.Get(ms, shareID).Extract()
			if err != nil {
				glog.Errorf("Failed to get share %v ", shareID)
			}
			if share.Status == "available" {
				fmt.Printf("Share: %v is available now\n", shareID)
				c <- nil
			}
			num++
		}
	}()
	return <-c
}

// DeleteManilaShare removes a share from manila which will cause it to be
// deleted on the storage server.
func DeleteManilaShare(ms *gophercloud.ServiceClient, shareID string) error {

	result := shares.Delete(ms, shareID)
	if result.Err != nil {
		glog.Errorf("Cannot delete volume %s: %v", shareID, result.Err)
		return result.Err
	}

	return nil
}
