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
	"fmt"
	"strconv"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

const (
	iscsiType      = "iscsi"
	secretRefCount = "secretRefCount"
)

var (
	secretLabel = map[string]string{"type": "iscsi"}
	chapSess    = []string{"node.session.auth.username", "node.session.auth.password"}
)

type iscsiMapper struct {
	volumeMapper
	cb clusterBroker
}

func getChapSecretName(connection volumeservice.VolumeConnection, options controller.VolumeOptions) string {
	if connection.Data.AuthMethod == "CHAP" {
		return "iscsi-secret-" + rand.String(5)
	}
	return ""
}

func (m *iscsiMapper) BuildPVSource(conn volumeservice.VolumeConnection, options controller.VolumeOptions, secretName string) (*v1.PersistentVolumeSource, error) {
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
		Name: secretName,
	}
	return ret, nil
}

func (m *iscsiMapper) AuthSetup(p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection) (string, error) {
	namespace := options.PVC.Namespace

	labelSelector := labels.SelectorFromSet(labels.Set(secretLabel))
	opts := metav1.ListOptions{LabelSelector: labelSelector.String()}
	secrets, err := p.Client.Core().Secrets(namespace).List(opts)
	if err != nil {
		return "", err
	}

	for _, sec := range secrets.Items {
		if m.sameSecretData(sec.Data, conn.Data) {
			err = m.handleSecretRefAndDeletion(p, &sec, nil, true)
			if err != nil {
				return "", err
			}
			return sec.Name, nil
		}
	}
	// Create a secret for the CHAP credentials
	secretName := getChapSecretName(conn, options)
	if secretName == "" {
		glog.V(3).Info("No CHAP authentication secret necessary")
		return "", nil
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{secretRefCount: strconv.Itoa(1)},
			Name:        secretName,
			Labels:      secretLabel,
		},
		Type: "kubernetes.io/iscsi-chap",
		Data: map[string][]byte{
			chapSess[0]: []byte(conn.Data.AuthUsername),
			chapSess[1]: []byte(conn.Data.AuthPassword),
		},
	}
	return secret.Name, m.cb.createSecret(p, namespace, secret)
}

func (m *iscsiMapper) AuthTeardown(p *cinderProvisioner, pv *v1.PersistentVolume) error {
	// Delete the CHAP credentials
	if pv.Spec.ISCSI.SecretRef == nil {
		glog.V(3).Info("No associated secret to delete")
		return nil
	}

	if pv.Annotations == nil || pv.Annotations[SecretRefered] == "" || pv.Annotations[SecretRefered] != "yes" {
		glog.V(3).Infof("SecretRef not refered by pv %s, no action", pv.Name)
		return nil
	}

	secretName := pv.Spec.ISCSI.SecretRef.Name
	secretNamespace := pv.Spec.ClaimRef.Namespace
	secret, err := p.Client.CoreV1().Secrets(secretNamespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return m.handleSecretRefAndDeletion(p, secret, pv, false)
}

func (m *iscsiMapper) sameSecretData(secretData map[string][]byte, data volumeservice.VolumeConnectionDetails) bool {
	if string(secretData[chapSess[0]]) == data.AuthUsername && string(secretData[chapSess[1]]) == data.AuthPassword {
		return true
	}
	return false
}

func (m *iscsiMapper) handleSecretRefAndDeletion(p *cinderProvisioner, secret *v1.Secret, pv *v1.PersistentVolume, add bool) error {
	oldSecret, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	if secret.Annotations == nil || secret.Annotations[secretRefCount] == "" {
		return fmt.Errorf("secret %s should have secretRefCount annotation", secret.Namespace+"/"+secret.Name)
	}
	count, err := strconv.Atoi(secret.Annotations[secretRefCount])
	if err != nil {
		return err
	}
	if add {
		count++
	} else if count == 1 {
		glog.V(2).Infof("secret %s no reference, delete it", secret.Namespace+"/"+secret.Name)
		if err != nil {
			return err
		}
		// update pv annotation 'SecretRefered' to "no"
		if err = m.updatePvSecretReferedAnnotation(p, pv); err != nil {
			return err
		}
		return m.cb.deleteSecret(p, secret.Namespace, secret.Name)
	} else {
		count--
		if err = m.updatePvSecretReferedAnnotation(p, pv); err != nil {
			return err
		}
	}
	secret.Annotations[secretRefCount] = strconv.Itoa(count)
	newSecret, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldSecret, newSecret, &v1.Secret{})
	if err != nil {
		return err
	}
	_, err = p.Client.CoreV1().Secrets(secret.Namespace).Patch(secret.Name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		glog.V(2).Infof("Failed to change annotation for secret %s: %v", secret.Name, err)
		return err
	}
	glog.V(2).Infof("Changed secretRefCount annotation for secret %s to %d", secret.Name, count)
	return nil
}

func (m *iscsiMapper) updatePvSecretReferedAnnotation(p *cinderProvisioner, pv *v1.PersistentVolume) error {
	if _, ok := pv.Annotations[SecretRefered]; !ok {
		return fmt.Errorf("no secretRefered in pv %s annotation", pv.Name)
	}
	oldPv, err := json.Marshal(pv)
	if err != nil {
		return err
	}
	pv.Annotations[SecretRefered] = "no"
	newPv, err := json.Marshal(pv)
	if err != nil {
		return err
	}
	patch, err := strategicpatch.CreateTwoWayMergePatch(oldPv, newPv, &v1.PersistentVolume{})
	if err != nil {
		return err
	}
	_, err = p.Client.Core().PersistentVolumes().Patch(pv.Name, types.StrategicMergePatchType, patch)
	if err != nil {
		glog.V(2).Infof("Failed to change annotation for pv %s: %v", pv.Name, err)
		return err
	}
	return nil
}
