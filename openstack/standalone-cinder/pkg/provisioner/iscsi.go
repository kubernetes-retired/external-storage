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
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const iscsiType = "iscsi"

type iscsiMapper struct {
	volumeMapper
}

func getChapSecretName(ctx provisionCtx) string {
	if ctx.Connection.Data.AuthMethod == "CHAP" {
		return ctx.Options.PVName + "-secret"
	}
	return ""
}

func (m *iscsiMapper) BuildPVSource(ctx provisionCtx) (*v1.PersistentVolumeSource, error) {
	ret := &v1.PersistentVolumeSource{
		ISCSI: &v1.ISCSIVolumeSource{
			// TODO: Need some way to specify the initiator name
			TargetPortal:    ctx.Connection.Data.TargetPortal,
			IQN:             ctx.Connection.Data.TargetIqn,
			Lun:             ctx.Connection.Data.TargetLun,
			SessionCHAPAuth: false,
		},
	}
	secretName := getChapSecretName(ctx)
	if secretName != "" {
		ret.ISCSI.SessionCHAPAuth = true
		ret.ISCSI.SecretRef = &v1.LocalObjectReference{
			Name: secretName,
		}
	}
	return ret, nil
}

func (m *iscsiMapper) AuthSetup(ctx provisionCtx) error {
	// Create a secret for the CHAP credentials
	secretName := getChapSecretName(ctx)
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
			"node.session.auth.username": []byte(ctx.Connection.Data.AuthUsername),
			"node.session.auth.password": []byte(ctx.Connection.Data.AuthPassword),
		},
	}
	namespace := ctx.Options.PVC.Namespace
	_, err := ctx.P.Client.CoreV1().Secrets(namespace).Create(secret)
	if err != nil {
		glog.Errorf("Failed to create chap secret in namespace %s: %v", namespace, err)
		return err
	}
	glog.V(3).Infof("Secret %s created", secretName)
	return nil
}

func (m *iscsiMapper) AuthTeardown(ctx deleteCtx) error {
	// Delete the CHAP credentials
	if ctx.PV.Spec.ISCSI.SecretRef == nil {
		glog.V(3).Info("No associated secret to delete")
		return nil
	}

	secretName := ctx.PV.Spec.ISCSI.SecretRef.Name
	secretNamespace := ctx.PV.Spec.ClaimRef.Namespace
	err := ctx.P.Client.CoreV1().Secrets(secretNamespace).Delete(secretName, nil)
	if err != nil {
		glog.Errorf("Failed to remove secret %s from namespace %s: %v", secretName, secretNamespace, err)
		return err
	}
	glog.V(3).Infof("Successfully deleted secret %s", secretName)
	return nil
}
