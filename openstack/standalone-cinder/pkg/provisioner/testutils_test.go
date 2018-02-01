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
	"bytes"
	"errors"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createVolumeOptions() controller.VolumeOptions {
	var storageClass = "storageclass"

	capacity, err := resource.ParseQuantity("1Gi")
	if err != nil {
		glog.Error("Programmer error, cannot parse quantity string")
		return controller.VolumeOptions{}
	}

	return controller.VolumeOptions{
		PVName: "testpv",
		PVC: &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "testns",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClass,
				AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceName(v1.ResourceStorage): capacity,
					},
				},
			},
		},
		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
	}
}

func createPersistentVolume() *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ProvisionerIDAnn: "identity",
				CinderVolumeID:   "cinderVolumeID",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{},
			ClaimRef: &v1.ObjectReference{
				Namespace: "testNs",
			},
		},
	}
}

func createCinderProvisioner() *cinderProvisioner {
	return &cinderProvisioner{
		Identity: "identity",
	}
}

type failureInjector struct {
	operationLog bytes.Buffer
	failOn       map[string]bool
}

func (fi *failureInjector) set(method string) {
	if fi.failOn == nil {
		fi.failOn = make(map[string]bool)
	}
	fi.failOn[method] = true
}

func (fi *failureInjector) isSet(method string) bool {
	if fi.failOn == nil {
		return false
	}
	value, ok := fi.failOn[method]
	if !ok {
		return false
	}
	return value
}

func (fi *failureInjector) ret(method string) error {
	if fi.isSet(method) {
		return errors.New("injected error for testing")
	}
	return nil
}

func (fi *failureInjector) logRet(fn string) error {
	if fi.isSet(fn) {
		return errors.New("injected error for testing")
	}
	fi.operationLog.WriteString(fn)
	fi.operationLog.WriteString(".")
	return nil
}

type fakeMapper struct {
	mightFail failureInjector
	volumeMapper
	failBuildPVSource bool
}

func (m *fakeMapper) BuildPVSource(conn volumeservice.VolumeConnection, options controller.VolumeOptions) (*v1.PersistentVolumeSource, error) {
	if m.failBuildPVSource {
		return nil, errors.New("Injected error for testing")
	}
	return &v1.PersistentVolumeSource{}, nil
}

func (m *fakeMapper) AuthSetup(p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection) error {
	return m.mightFail.ret("AuthSetup")
}

func (m *fakeMapper) AuthTeardown(p *cinderProvisioner, pv *v1.PersistentVolume) error {
	return m.mightFail.ret("AuthTeardown")
}

type fakeClusterBroker struct {
	clusterBroker
	CreatedSecret *v1.Secret
	DeletedSecret string
	Namespace     string
}

func (cb *fakeClusterBroker) createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error {
	cb.CreatedSecret = secret
	cb.Namespace = ns
	return nil
}

func (cb *fakeClusterBroker) deleteSecret(p *cinderProvisioner, ns string, secretName string) error {
	cb.DeletedSecret = secretName
	cb.Namespace = ns
	return nil
}
