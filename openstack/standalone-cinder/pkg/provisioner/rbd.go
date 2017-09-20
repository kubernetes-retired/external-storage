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
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
)

const rbdType = "rbd"

type rbdMapper struct {
	volumeMapper
}

func getMonitors(conn volumeservice.VolumeConnection) []string {
	if len(conn.Data.Hosts) != len(conn.Data.Ports) {
		glog.Errorf("Error parsing rbd connection info: 'hosts' and 'ports' have different lengths")
		return nil
	}
	mons := make([]string, len(conn.Data.Hosts))
	for i := range conn.Data.Hosts {
		mons[i] = fmt.Sprintf("%s:%s", conn.Data.Hosts[i], conn.Data.Ports[i])
	}
	return mons
}

func getRbdSecretName(ctx provisionCtx) string {
	return fmt.Sprintf("%s-cephx-secret", *ctx.Options.PVC.Spec.StorageClassName)
}

func (m *rbdMapper) BuildPVSource(ctx provisionCtx) (*v1.PersistentVolumeSource, error) {
	mons := getMonitors(ctx.Connection)
	if mons == nil {
		return nil, errors.New("No monitors could be parsed from connection info")
	}
	splitName := strings.SplitN(ctx.Connection.Data.Name, "/", 2)
	if len(splitName) != 2 {
		return nil, errors.New("Field 'name' cannot be split into pool and image")
	}

	return &v1.PersistentVolumeSource{
		RBD: &v1.RBDVolumeSource{
			CephMonitors: mons,
			RBDPool:      splitName[0],
			RBDImage:     splitName[1],
			RadosUser:    ctx.Connection.Data.AuthUsername,
			SecretRef: &v1.LocalObjectReference{
				Name: getRbdSecretName(ctx),
			},
		},
	}, nil
}

func (m *rbdMapper) AuthSetup(ctx provisionCtx) error {
	return nil
}

func (m *rbdMapper) AuthTeardown(ctx deleteCtx) error {
	return nil
}
