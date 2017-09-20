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
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type volumeMapper interface {
	BuildPVSource(ctx provisionCtx) (*v1.PersistentVolumeSource, error)
	AuthSetup(ctx provisionCtx) error
	AuthTeardown(ctx deleteCtx) error
}

func newVolumeMapperFromConnection(conn volumeservice.VolumeConnection) (volumeMapper, error) {
	switch conn.DriverVolumeType {
	default:
		msg := fmt.Sprintf("Unsupported persistent volume type: %s", conn.DriverVolumeType)
		return nil, errors.New(msg)
	case iscsiType:
		return new(iscsiMapper), nil
	case rbdType:
		return new(rbdMapper), nil
	}
}

func newVolumeMapperFromPV(ctx deleteCtx) (volumeMapper, error) {
	if ctx.PV.Spec.ISCSI != nil {
		return new(iscsiMapper), nil
	} else if ctx.PV.Spec.RBD != nil {
		return new(rbdMapper), nil
	} else {
		return nil, errors.New("Unsupported persistent volume source")
	}
}

func buildPV(m volumeMapper, ctx provisionCtx, volumeID string) (*v1.PersistentVolume, error) {
	pvSource, err := m.BuildPVSource(ctx)
	if err != nil {
		glog.Errorf("Failed to build PV Source element: %v", err)
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ctx.Options.PVName,
			Namespace: ctx.Options.PVC.Namespace,
			Annotations: map[string]string{
				ProvisionerIDAnn: ctx.P.Identity,
				CinderVolumeID:   volumeID,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: ctx.Options.PersistentVolumeReclaimPolicy,
			AccessModes:                   ctx.Options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): ctx.Options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: *pvSource,
		},
	}
	return pv, nil
}
