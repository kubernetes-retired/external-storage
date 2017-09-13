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
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const iscsiType = "iscsi"
const initiatorName = "iqn.1994-05.com.redhat:a13fc3d1cc22"

type iscsiMapper struct {
	volumeMapper
}

func getChapSecretName(ctx provisionCtx) string {
	if ctx.connection.Data.AuthMethod == "CHAP" {
		return ctx.options.PVName + "-secret"
	}
	return ""
}

func (m *iscsiMapper) BuildPVSource(ctx provisionCtx) (*v1.PersistentVolumeSource, error) {
	ret := &v1.PersistentVolumeSource{
		ISCSI: &v1.ISCSIVolumeSource{
			// TODO: Need some way to specify the initiator name
			TargetPortal:    ctx.connection.Data.TargetPortal,
			IQN:             ctx.connection.Data.TargetIqn,
			Lun:             ctx.connection.Data.TargetLun,
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
		glog.Info("No CHAP authentication secret necessary")
		return nil
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Type: "kubernetes.io/iscsi-chap",
		Data: map[string][]byte{
			"node.session.auth.username": []byte(ctx.connection.Data.AuthUsername),
			"node.session.auth.password": []byte(ctx.connection.Data.AuthPassword),
		},
	}
	namespace := ctx.options.PVC.Namespace
	_, err := ctx.p.client.CoreV1().Secrets(namespace).Create(secret)
	if err != nil {
		glog.Errorf("Failed to create chap secret: %v", err)
		return err
	}
	glog.Infof("Secret %s created", secretName)
	return nil
}

func (m *iscsiMapper) AuthTeardown(ctx deleteCtx) error {
	// Delete the CHAP credentials
	if ctx.pv.Spec.ISCSI.SecretRef == nil {
		glog.Info("No associated secret to delete")
		return nil
	}

	secretName := ctx.pv.Spec.ISCSI.SecretRef.Name
	secretNamespace := ctx.pv.Spec.ClaimRef.Namespace
	err := ctx.p.client.CoreV1().Secrets(secretNamespace).Delete(secretName, nil)
	if err != nil {
		glog.Errorf("Failed to remove secret: %s, %v", secretName, err)
		return err
	}
	glog.Infof("Successfully deleted secret %s", secretName)
	return nil
}
