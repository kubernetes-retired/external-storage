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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
)

const iscsiType = "iscsi"

var (
	secretLabel = map[string]string{"type": "iscsi"}
	chap_sess   = []string{"node.session.auth.username", "node.session.auth.password"}
)

type iscsiMapper struct {
	volumeMapper
	cb clusterBroker
}

func getChapSecretName(connection volumeservice.VolumeConnection, options controller.VolumeOptions) string {
	if connection.Data.AuthMethod == "CHAP" {
		return "iscsi-secret-" + rand.String(3)
	}
	return ""
}

func (m *iscsiMapper) BuildPVSource(conn volumeservice.VolumeConnection, options controller.VolumeOptions, secret *v1.Secret) (*v1.PersistentVolumeSource, error) {
	ret := &v1.PersistentVolumeSource{
		ISCSI: &v1.ISCSIVolumeSource{
			// TODO: Need some way to specify the initiator name
			TargetPortal:    conn.Data.TargetPortal,
			IQN:             conn.Data.TargetIqn,
			Lun:             conn.Data.TargetLun,
			SessionCHAPAuth: false,
		},
	}
	ret.ISCSI.SessionCHAPAuth = true
	ret.ISCSI.SecretRef = &v1.LocalObjectReference{
		Name: secret.Name,
	}
	return ret, nil
}

func (m *iscsiMapper) AuthSetup(p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection) (*v1.Secret, error) {
	namespace := options.PVC.Namespace

	labelSelector := labels.SelectorFromSet(labels.Set(secretLabel))
	opts := metav1.ListOptions{LabelSelector: labelSelector.String()}
	secrets, err := p.Client.Core().Secrets(namespace).List(opts)
	if err != nil {
		return nil, err
	}

	for _, sec := range secrets.Items {
		if m.sameSecretData(sec.Data, conn.Data) {
			return &sec, nil
		}
	}
	// Create a secret for the CHAP credentials
	secretName := getChapSecretName(conn, options)
	if secretName == "" {
		glog.V(3).Info("No CHAP authentication secret necessary")
		return nil, nil
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   secretName,
			Labels: secretLabel,
		},
		Type: "kubernetes.io/iscsi-chap",
		Data: map[string][]byte{
			"node.session.auth.username": []byte(conn.Data.AuthUsername),
			"node.session.auth.password": []byte(conn.Data.AuthPassword),
		},
	}
	return secret, m.cb.createSecret(p, namespace, secret)
}

func (m *iscsiMapper) AuthTeardown(p *cinderProvisioner, pv *v1.PersistentVolume) error {
	// Delete the CHAP credentials
	//if pv.Spec.ISCSI.SecretRef == nil {
	//	glog.V(3).Info("No associated secret to delete")
	//	return nil
	//}
	//
	//secretName := pv.Spec.ISCSI.SecretRef.Name
	//secretNamespace := pv.Spec.ClaimRef.Namespace
	//return m.cb.deleteSecret(p, secretNamespace, secretName)
	return nil
}

func (m *iscsiMapper) sameSecretData(secretData map[string][]byte, data volumeservice.VolumeConnectionDetails) bool {
	if string(secretData[chap_sess[0]]) == data.AuthUsername && string(secretData[chap_sess[1]]) == data.AuthPassword {
		return true
	}
	return false
}
