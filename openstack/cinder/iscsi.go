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
	"k8s.io/api/core/v1"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)


const ISCSI_TYPE = "iscsi"
const INITIATOR_NAME = "iqn.1994-05.com.redhat:a13fc3d1cc22"


type iscsiMapper struct {
	volumeMapper
}


func newIscsiMapper(p *cinderProvisioner, volumeId string) *iscsiMapper {
	m := &iscsiMapper{}
	m.p = p
	m.cinderVolumeId = volumeId
	return m
}


func getSecretName(volumeId string) string {
	return volumeId + "-secret"
}

func (m *iscsiMapper) BuildPVSource(options controller.VolumeOptions, conn volumeConnection) (*v1.PersistentVolumeSource, error) {
	return &v1.PersistentVolumeSource{
		ISCSI: &v1.ISCSIVolumeSource{
			// TODO: Need some way to specify the initiator name
			TargetPortal: conn.Data.TargetPortal,
			IQN: conn.Data.TargetIqn,
			Lun: conn.Data.TargetLun,
			SessionCHAPAuth: true,
			SecretRef: &v1.LocalObjectReference{
				Name: getSecretName(m.cinderVolumeId),
			},
		},
	}, nil
}

func (m *iscsiMapper) AuthSetup(options controller.VolumeOptions, conn volumeConnection) error {
	// Create a secret for the CHAP credentials
	secretName := getSecretName(m.cinderVolumeId)
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Type: "kubernetes.io/iscsi-chap",
		Data: map[string][]byte{
			"node.session.auth.username": []byte(conn.Data.AuthUsername),
			"node.session.auth.password": []byte(conn.Data.AuthPassword),
		},
	}
	namespace := options.PVC.Namespace
	_, err := m.p.client.CoreV1().Secrets(namespace).Create(secret)
	if err != nil {
		glog.Errorf("Failed to create chap secret: %v", err)
		return err
	}
	glog.Infof("Secret %s created", secretName)
	return nil
}

func (m *iscsiMapper) AuthTeardown(pv *v1.PersistentVolume) error {
	// Delete the CHAP credentials
	secretName := pv.Spec.ISCSI.SecretRef.Name
	secretNamespace := pv.Spec.ClaimRef.Namespace
	err := m.p.client.CoreV1().Secrets(secretNamespace).Delete(secretName, nil)
	if err != nil {
		glog.Errorf("Failed to remove secret: %s, %v", secretName, err)
		return err
	} else{
		glog.Infof("Successfully deleted secret %s", secretName)
		return nil
	}
}