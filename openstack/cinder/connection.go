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
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/extensions/volumeactions"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/pkg/errors"
)

const INITIATOR_NAME = "iqn.1994-05.com.redhat:a13fc3d1cc22"

type volumeConnectionDetails struct {
	VolumeId string `json:"volume_id"`

	AuthMethod string `json:"auth_method"`
	AuthUsername string `json:"auth_username"`
	AuthPassword string `json:"auth_password"`

	TargetPortal string `json:"target_portal"`
	TargetIqn string `json:"target_iqn"`
	TargetLun int32 `json:"target_lun"`
}

type volumeConnection struct {
	DriverVolumeType string `json:"driver_volume_type"`
	Data volumeConnectionDetails `json:"data"`
}

type rcvVolumeConnection struct {
	ConnectionInfo volumeConnection `json:"connection_info"`
}

func (p *cinderProvisioner) connectVolume(volumeId string) (volumeConnection, error) {
	cClient, err := p.cloud.NewBlockStorageV2()
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
		err := volumeactions.InitializeConnection(cClient, volumeId, &opt).ExtractInto(&rcv)
		if err != nil {
			glog.Errorf("failed to initialize connection :%v", err)
			c <- err
		} else {
			glog.Infof("Received connection info: %v", rcv)
			close(c)
		}
	})
	err = <- c
	if err != nil {
		return volumeConnection{}, err
	} else {
		return rcv.ConnectionInfo, nil
	}
}


func (p *cinderProvisioner) disconnectVolume(volumeId string) error {
	cClient, err := p.cloud.NewBlockStorageV2()
	if err != nil {
		glog.Errorf("failed to get cinder client: %v", err)
		return err
	}
	opt := volumeactions.TerminateConnectionOpts{
		Host:      "localhost",
		IP:        "127.0.0.1",
		Initiator: INITIATOR_NAME,
	}

	err = volumeactions.TerminateConnection(cClient, volumeId, &opt).Result.Err
	if err != nil {
		glog.Errorf("Failed to terminate connection to volume %s: %v",
			volumeId, err)
		return err
	}

	return nil
}

func getSecretName(volumeId string) string {
	return volumeId + "-secret"
}

func (p *cinderProvisioner) createIscsiChapSecret(options controller.VolumeOptions, connInfo volumeConnection) error {
	secretName := getSecretName(connInfo.Data.VolumeId)
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Type: "kubernetes.io/iscsi-chap",
		Data: map[string][]byte{
			"node.session.auth.username": []byte(connInfo.Data.AuthUsername),
			"node.session.auth.password": []byte(connInfo.Data.AuthPassword),
		},
	}
	namespace := options.PVC.Namespace
	_, err := p.client.CoreV1().Secrets(namespace).Create(secret)
	if err != nil {
		glog.Errorf("Failed to create chap secret: %v", err)
		return err
	}
	glog.Infof("Secret %s created", secretName)
	return nil
}


func (p *cinderProvisioner) buildIscsiPersistentVolume(options controller.VolumeOptions, connInfo volumeConnection) (*v1.PersistentVolume, error) {
	err := p.createIscsiChapSecret(options, connInfo)
	if err != nil {
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Namespace: options.PVC.Namespace,
			Annotations: map[string]string{
				provisionerIDAnn: p.identity,
				cinderVolumeId: connInfo.Data.VolumeId,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				ISCSI: &v1.ISCSIVolumeSource{
					// TODO: Need some way to specify the initiator name
					TargetPortal: connInfo.Data.TargetPortal,
					IQN: connInfo.Data.TargetIqn,
					Lun: connInfo.Data.TargetLun,
					SessionCHAPAuth: true,
					SecretRef: &v1.LocalObjectReference{
						Name: getSecretName(connInfo.Data.VolumeId),
					},
				},
			},
		},
	}
	return pv, nil
}

func (p *cinderProvisioner) buildPersistentVolume(options controller.VolumeOptions, connInfo volumeConnection) (*v1.PersistentVolume, error) {
	if connInfo.DriverVolumeType == "iscsi" {
		return p.buildIscsiPersistentVolume(options, connInfo)
	} else {
		return nil, errors.New("Unsupported cinder volume type: " + connInfo.DriverVolumeType)
	}
}
