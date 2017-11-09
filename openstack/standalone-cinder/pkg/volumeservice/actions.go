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

package volumeservice

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/extensions/volumeactions"
	volumes_v2 "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

const initiatorName = "iqn.1994-05.com.redhat:a13fc3d1cc22"

// VolumeConnectionDetails represent the type-specific values for a given
// DriverVolumeType.  Depending on the volume type, fields may be absent or
// have a semantically different meaning.
type VolumeConnectionDetails struct {
	VolumeID string `json:"volume_id"`
	Name     string `json:"name"`

	AuthMethod   string `json:"auth_method"`
	AuthUsername string `json:"auth_username"`
	AuthPassword string `json:"auth_password"`
	SecretType   string `json:"secret_type"`

	TargetPortal string `json:"target_portal"`
	TargetIqn    string `json:"target_iqn"`
	TargetLun    int32  `json:"target_lun"`

	ClusterName string   `json:"cluster_name"`
	Hosts       []string `json:"hosts"`
	Ports       []string `json:"ports"`
}

// VolumeConnection represents the connection information returned from the
// cinder os-initialize_connection API call
type VolumeConnection struct {
	DriverVolumeType string                  `json:"driver_volume_type"`
	Data             VolumeConnectionDetails `json:"data"`
}

type rcvVolumeConnection struct {
	ConnectionInfo VolumeConnection `json:"connection_info"`
}

// CreateCinderVolume creates a new volume in cinder according to the PVC specifications
func CreateCinderVolume(vs *gophercloud.ServiceClient, options controller.VolumeOptions) (string, error) {
	name := fmt.Sprintf("cinder-dynamic-pvc-%s", uuid.NewUUID())
	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	sizeBytes := capacity.Value()
	// Cinder works with gigabytes, convert to GiB with rounding up
	sizeGB := int((sizeBytes + 1024*1024*1024 - 1) / (1024 * 1024 * 1024))
	volType := ""
	availability := "nova"
	// Apply ProvisionerParameters (case-insensitive). We leave validation of
	// the values to the cloud provider.
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "type":
			volType = v
		case "availability":
			availability = v
		default:
			return "", fmt.Errorf("invalid option %q", k)
		}
	}

	opts := volumes_v2.CreateOpts{
		Name:             name,
		Size:             sizeGB,
		VolumeType:       volType,
		AvailabilityZone: availability,
	}

	vol, err := volumes_v2.Create(vs, &opts).Extract()

	if err != nil {
		glog.Errorf("Failed to create a %d GiB volume: %v", sizeGB, err)
		return "", err
	}

	glog.V(2).Infof("Created volume %v in Availability Zone: %v", vol.ID, vol.AvailabilityZone)
	return vol.ID, nil
}

// WaitForAvailableCinderVolume waits for a newly created cinder volume to
// become available.  The connection information cannot be retrieved from cinder
// until the volume is available.
func WaitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	// TODO: Implement proper polling instead of brain-dead timers
	c := make(chan error)
	go time.AfterFunc(5*time.Second, func() {
		c <- nil
	})
	return <-c
}

// ReserveCinderVolume marks the volume as 'Attaching' in cinder.  This prevents
// the volume from being used for another purpose.
func ReserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeactions.Reserve(vs, volumeID).ExtractErr()
}

// ConnectCinderVolume retrieves connection information for a cinder volume.
// Depending on the type of volume, cinder may perform setup on a storage server
// such as mapping a LUN to a particular ISCSI initiator.
func ConnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (VolumeConnection, error) {
	opt := volumeactions.InitializeConnectionOpts{
		Host:      "localhost",
		IP:        "127.0.0.1",
		Initiator: initiatorName,
	}
	var rcv rcvVolumeConnection
	err := volumeactions.InitializeConnection(vs, volumeID, &opt).ExtractInto(&rcv)
	if err != nil {
		glog.Errorf("failed to initialize connection :%v", err)
		return VolumeConnection{}, err
	}
	glog.V(3).Infof("Received connection info: %v", rcv)
	return rcv.ConnectionInfo, nil
}

// DisconnectCinderVolume removes a connection to a cinder volume.  Depending on
// the volume type, this may cause cinder to clean up the connection at a
// storage server (i.e. remove a LUN mapping).
func DisconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	opt := volumeactions.TerminateConnectionOpts{
		Host:      "localhost",
		IP:        "127.0.0.1",
		Initiator: initiatorName,
	}

	err := volumeactions.TerminateConnection(vs, volumeID, &opt).Result.Err
	if err != nil {
		glog.Errorf("Failed to terminate connection to volume %s: %v",
			volumeID, err)
		return err
	}

	return nil
}

// UnreserveCinderVolume marks a cinder volume in 'Attaching' state as 'Available'.
func UnreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return volumeactions.Unreserve(vs, volumeID).ExtractErr()
}

// DeleteCinderVolume removes a volume from cinder which will cause it to be
// deleted on the storage server.
func DeleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	err := volumes_v2.Delete(vs, volumeID).ExtractErr()
	if err != nil {
		glog.Errorf("Cannot delete volume %s: %v", volumeID, err)
	}

	return err
}
