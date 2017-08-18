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

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/openstack"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/extensions/volumeactions"
)


type volumeConnectionDetails struct {
	VolumeId string `json:"volume_id"`
	Name string `json:"name"`

	AuthMethod string `json:"auth_method"`
	AuthUsername string `json:"auth_username"`
	AuthPassword string `json:"auth_password"`
	SecretType string `json:"secret_type"`

	TargetPortal string `json:"target_portal"`
	TargetIqn string `json:"target_iqn"`
	TargetLun int32 `json:"target_lun"`

	ClusterName string `json:"cluster_name"`
	Hosts []string `json:"hosts"`
	Ports []string `json:"ports"`
}


type volumeConnection struct {
	DriverVolumeType string `json:"driver_volume_type"`
	Data volumeConnectionDetails `json:"data"`
}


type rcvVolumeConnection struct {
	ConnectionInfo volumeConnection `json:"connection_info"`
}


type cinderVolume struct {
	os openstack.OpenStack
	id string
}


func newCinderVolume(os openstack.OpenStack, id string) *cinderVolume{
	return &cinderVolume{
		os: os,
		id: id,
	}
}


func createCinderVolume(os openstack.OpenStack, options controller.VolumeOptions) (*cinderVolume, error) {
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
			return nil, fmt.Errorf("invalid option %q", k)
		}
	}

	//TODO udpate kubernetes vendor and fix AZ
	volumeId, _, err := os.CreateVolume(name, sizeGB, volType, availability, nil)
	if err != nil {
		glog.Infof("Error creating cinder volume: %v", err)
		return nil, err
	}
	glog.Infof("Successfully created cinder volume %s", volumeId)
	return &cinderVolume{
		os: os,
		id: volumeId,
	}, nil
}


func (v *cinderVolume) connect() (volumeConnection, error) {
	cClient, err := v.os.NewBlockStorageV2()
	if err != nil {
		glog.Infof("failed to get cinder client: %v", err)
		return volumeConnection{}, err
	}
	opt := volumeactions.InitializeConnectionOpts{
		Host:      "localhost",
		IP:        "127.0.0.1",
		Initiator: INITIATOR_NAME,
	}

	// TODO: Implement proper polling instead of brain-dead timers
	c := make(chan error)
	var rcv rcvVolumeConnection

	go time.AfterFunc(5 * time.Second, func() {
		err := volumeactions.InitializeConnection(cClient, v.id, &opt).ExtractInto(&rcv)
		if err != nil {
			glog.Errorf("failed to initialize connection :%v", err)
			c <- err
		} else {
			glog.Infof("Received connection info: %v", rcv)
			close(c)
		}
	})
	err = <-c
	if err != nil {
		return volumeConnection{}, err
	}
	return rcv.ConnectionInfo, nil
}


func (v *cinderVolume) disconnect() error {
	cClient, err := v.os.NewBlockStorageV2()
	if err != nil {
		glog.Errorf("failed to get cinder client: %v", err)
		return err
	}
	opt := volumeactions.TerminateConnectionOpts{
		Host:      "localhost",
		IP:        "127.0.0.1",
		Initiator: INITIATOR_NAME,
	}

	err = volumeactions.TerminateConnection(cClient, v.id, &opt).Result.Err
	if err != nil {
		glog.Errorf("Failed to terminate connection to volume %s: %v",
			v.id, err)
		return err
	}

	return nil
}


type volumeMapper interface {
	BuildPVSource(ctx provisionCtx) (*v1.PersistentVolumeSource, error)
	AuthSetup(ctx provisionCtx) error
	AuthTeardown(ctx deleteCtx) error
}


type mapperContext struct {
	cinderVolumeId string
	p              *cinderProvisioner
}


func newVolumeMapperFromConnection(conn volumeConnection) (volumeMapper, error) {
	switch conn.DriverVolumeType {
	default:
		msg := fmt.Sprintf("Unsupported volume type: %s", conn.DriverVolumeType)
		return nil, errors.New(msg)
	case ISCSI_TYPE:
		return new(iscsiMapper), nil
	case RBD_TYPE:
		return new(rbdMapper), nil
	}
}


func newVolumeMapperFromPV(ctx deleteCtx) (volumeMapper, error) {
	if ctx.pv.Spec.ISCSI != nil {
		return new(iscsiMapper), nil
	} else if ctx.pv.Spec.RBD != nil {
		return new(rbdMapper), nil
	} else {
		return nil, errors.New("Unsupported volume source")
	}
}


func BuildPV(m volumeMapper, ctx provisionCtx) (*v1.PersistentVolume, error) {
	pvSource, err := m.BuildPVSource(ctx)
	if err != nil {
		glog.Errorf("Failed to build PV Source element: %v", err)
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: ctx.options.PVName,
			Namespace: ctx.options.PVC.Namespace,
			Annotations: map[string]string{
				provisionerIDAnn: ctx.p.identity,
				cinderVolumeId: ctx.connection.Data.VolumeId,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: ctx.options.PersistentVolumeReclaimPolicy,
			AccessModes:                   ctx.options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): ctx.options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: *pvSource,
		},
	}
	return pv, nil
}
