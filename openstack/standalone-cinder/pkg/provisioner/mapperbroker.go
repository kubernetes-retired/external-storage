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
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	"k8s.io/api/core/v1"
)

type mapperBroker interface {
	newVolumeMapperFromConnection(conn volumeservice.VolumeConnection) (volumeMapper, error)
	newVolumeMapperFromPV(pv *v1.PersistentVolume) (volumeMapper, error)
	buildPV(m volumeMapper, p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection, volumeID string) (*v1.PersistentVolume, error)
}

type volumeMapperBroker struct {
	mapperBroker
}

func (mb *volumeMapperBroker) newVolumeMapperFromConnection(conn volumeservice.VolumeConnection) (volumeMapper, error) {
	return newVolumeMapperFromConnection(conn)
}

func (mb *volumeMapperBroker) newVolumeMapperFromPV(pv *v1.PersistentVolume) (volumeMapper, error) {
	return newVolumeMapperFromPV(pv)
}

func (mb *volumeMapperBroker) buildPV(m volumeMapper, p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection, volumeID string) (*v1.PersistentVolume, error) {
	return buildPV(m, p, options, conn, volumeID)
}
