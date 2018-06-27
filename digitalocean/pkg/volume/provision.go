/*
Copyright 2016 The Kubernetes Authors.

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

package volume

import (
	"context"
	"fmt"

	"github.com/digitalocean/godo"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/kubelet/apis"
)

const (
	flexvolumeVendor = "external-storage"
	flexvolumeDriver = "digitalocean"

	// are we allowed to set this? else make up our own
	annCreatedBy = "kubernetes.io/createdby"
	createdBy    = "digitalocean-provisioner"

	annVolumeID = "digitalocean.external-storage.incubator.kubernetes.io/VolumeID"

	// A PV annotation for the identity of the s3fsProvisioner that provisioned it
	annProvisionerID = "Provisioner_Id"
)

// NewDigitalOceanProvisioner creates a new DigitalOcean provisioner
func NewDigitalOceanProvisioner(ctx context.Context, client kubernetes.Interface, doClient *godo.Client) controller.Provisioner {
	var identity types.UID

	provisioner := &digitaloceanProvisioner{
		client:   client,
		doClient: doClient,
		ctx:      ctx,
		identity: identity,
	}

	return provisioner
}

type digitaloceanProvisioner struct {
	client   kubernetes.Interface
	doClient *godo.Client
	ctx      context.Context
	identity types.UID
}

var _ controller.Provisioner = &digitaloceanProvisioner{}

// https://github.com/kubernetes-incubator/external-storage/blob/e26435c2ccd9ed5d2a60c838a902d22a3ec6ef5c/iscsi/targetd/provisioner/iscsi-provisioner.go#L102
// getAccessModes returns access modes DigitalOcean Block Storage volume supports.
func (p *digitaloceanProvisioner) getAccessModes() []v1.PersistentVolumeAccessMode {
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
	}
}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume.
func (p *digitaloceanProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if !util.AccessModesContainedInAll(p.getAccessModes(), options.PVC.Spec.AccessModes) {
		return nil, fmt.Errorf("Invalid Access Modes: %v, Supported Access Modes: %v", options.PVC.Spec.AccessModes, p.getAccessModes())
	}

	vol, err := p.createVolume(options)
	if err != nil {
		return nil, err
	}

	annotations := make(map[string]string)
	annotations[annCreatedBy] = createdBy

	annotations[annProvisionerID] = string(p.identity)
	annotations[annVolumeID] = vol.ID

	labels := make(map[string]string)
	labels[apis.LabelZoneFailureDomain] = vol.Region.Slug
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        options.PVName,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): resource.MustParse(fmt.Sprintf("%dGi", vol.SizeGigaBytes)),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{

				FlexVolume: &v1.FlexPersistentVolumeSource{
					Driver:   fmt.Sprintf("%s/%s", flexvolumeVendor, flexvolumeDriver),
					Options:  map[string]string{},
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

func (p *digitaloceanProvisioner) createVolume(volumeOptions controller.VolumeOptions) (*godo.Volume, error) {
	zone, ok := volumeOptions.Parameters["zone"]
	if !ok {
		return nil, fmt.Errorf("Error zone parameter missing")
	}

	// https://github.com/kubernetes-incubator/external-storage/blob/04d64584da09cc16ef0bd590775ef12415b663c9/gluster/block/cmd/glusterblock-provisioner/glusterblock-provisioner.go#L183
	// https://developers.digitalocean.com/documentation/v2/#create-a-new-block-storage-volume
	volSize := volumeOptions.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := volSize.Value()
	volszInt := util.RoundUpSize(volSizeBytes, util.GiB)

	createRequest := &godo.VolumeCreateRequest{
		Region:        zone,
		Name:          volumeOptions.PVName,
		SizeGigaBytes: volszInt,
	}

	vol, _, err := p.doClient.Storage.CreateVolume(p.ctx, createRequest)
	if err != nil {
		glog.Errorf("Failed to create volume %s, error: %s", volumeOptions, err.Error())
		return nil, err
	}
	return vol, nil
}
