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
	"errors"
	"fmt"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type volumeMapper interface {
	// BuildPVSource should build a PersistentVolumeSource from cinder connection
	// information and context from the cluster such as the PVC.
	BuildPVSource(conn volumeservice.VolumeConnection, options controller.VolumeOptions) (*v1.PersistentVolumeSource, error)
	// AuthSetup should perform any authentication setup such as secret creation
	// that would be required before a host can connect to the volume.
	AuthSetup(p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection) error
	// AuthTeardown should perform any necessary cleanup related to authentication
	// as the volume is being deleted.
	AuthTeardown(p *cinderProvisioner, pv *v1.PersistentVolume) error
}

func newVolumeMapperFromConnection(conn volumeservice.VolumeConnection) (volumeMapper, error) {
	switch conn.DriverVolumeType {
	default:
		msg := fmt.Sprintf("Unsupported persistent volume type: %s", conn.DriverVolumeType)
		return nil, errors.New(msg)
	case iscsiType:
		return &iscsiMapper{cb: &k8sClusterBroker{}}, nil
	case rbdType:
		return new(rbdMapper), nil
	}
}

func newVolumeMapperFromPV(pv *v1.PersistentVolume) (volumeMapper, error) {
	if pv.Spec.ISCSI != nil {
		return &iscsiMapper{cb: &k8sClusterBroker{}}, nil
	} else if pv.Spec.RBD != nil {
		return new(rbdMapper), nil
	} else {
		return nil, errors.New("Unsupported persistent volume source")
	}
}

func buildPV(m volumeMapper, p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection, volumeID string) (*v1.PersistentVolume, error) {
	pvSource, err := m.BuildPVSource(conn, options)
	if err != nil {
		glog.Errorf("Failed to build PV Source element: %v", err)
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      options.PVName,
			Namespace: options.PVC.Namespace,
			Annotations: map[string]string{
				ProvisionerIDAnn: p.Identity,
				CinderVolumeID:   volumeID,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: *pvSource,
		},
	}
	return pv, nil
}
