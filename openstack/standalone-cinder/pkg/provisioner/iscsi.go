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

package provisioner

import (
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const iscsiType = "iscsi"

type iscsiMapper struct {
	volumeMapper
	cb clusterBroker
}

func getChapSecretName(connection volumeservice.VolumeConnection, options controller.VolumeOptions) string {
	if connection.Data.AuthMethod == "CHAP" {
		return options.PVName + "-secret"
	}
	return ""
}

func (m *iscsiMapper) BuildPVSource(conn volumeservice.VolumeConnection, options controller.VolumeOptions) (*v1.PersistentVolumeSource, error) {
	ret := &v1.PersistentVolumeSource{
		ISCSI: &v1.ISCSIPersistentVolumeSource{
			// TODO: Need some way to specify the initiator name
			TargetPortal:    conn.Data.TargetPortal,
			IQN:             conn.Data.TargetIqn,
			Lun:             conn.Data.TargetLun,
			SessionCHAPAuth: false,
		},
	}
	secretName := getChapSecretName(conn, options)
	if secretName != "" {
		ret.ISCSI.SessionCHAPAuth = true
		ret.ISCSI.SecretRef = &v1.SecretReference{
			Name: secretName,
		}
	}
	return ret, nil
}

func (m *iscsiMapper) AuthSetup(p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection) error {
	// Create a secret for the CHAP credentials
	secretName := getChapSecretName(conn, options)
	if secretName == "" {
		glog.V(3).Info("No CHAP authentication secret necessary")
		return nil
	}
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
	return m.cb.createSecret(p, namespace, secret)
}

func (m *iscsiMapper) AuthTeardown(p *cinderProvisioner, pv *v1.PersistentVolume) error {
	// Delete the CHAP credentials
	if pv.Spec.ISCSI.SecretRef == nil {
		glog.V(3).Info("No associated secret to delete")
		return nil
	}

	secretName := pv.Spec.ISCSI.SecretRef.Name
	secretNamespace := pv.Spec.ClaimRef.Namespace
	return m.cb.deleteSecret(p, secretNamespace, secretName)
}
