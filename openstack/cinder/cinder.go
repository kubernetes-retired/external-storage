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
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/openstack/blockstorage/extensions/volumeactions"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/openstack"
)

const (
	provisionerName  = "openstack.org/cinder-baremetal"
	provisionerIDAnn = "cinderBaremetalProvisionerIdentity"
)

type cinderProvisioner struct {
	// OpenStack cloud provider
	cloud openstack.OpenStack
	// Kubernetes Client. Use to create secret
	client kubernetes.Interface
	// Identity of this cinderProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string
}

func newCinderProvisioner(client kubernetes.Interface, id, configFilePath string) (controller.Provisioner, error) {
	cloud, err := cloudprovider.InitCloudProvider("openstack", configFilePath)
	if err != nil {
		return nil, err
	}
	os, ok := cloud.(*openstack.OpenStack)
	if !ok || cloud == nil {
		return nil, fmt.Errorf("failed to get openstack cloud provider")
	}
	return &cinderProvisioner{
		cloud:    *os,
		client:   client,
		identity: id,
	}, nil
}

var _ controller.Provisioner = &cinderProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *cinderProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := capacity.Value()
	// Cinder works with gigabytes, convert to GiB with rounding up
	volSizeGB := int((volSizeBytes + 1024*1024*1024 - 1) / (1024 * 1024 * 1024))
	name := fmt.Sprintf("cinder-dynamic-pvc-%s", uuid.NewUUID())
	vtype := ""
	availability := "nova"
	// Apply ProvisionerParameters (case-insensitive). We leave validation of
	// the values to the cloud provider.
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "type":
			vtype = v
		case "availability":
			availability = v
		default:
			return nil, fmt.Errorf("invalid option %q", k)
		}
	}
	//TODO udpate kubernetes vendor and fix AZ
	volumeId, _, err := p.cloud.CreateVolume(name, volSizeGB, vtype, availability, nil)
	if err != nil {
		glog.Infof("Error creating cinder volume: %v", err)
		return nil, err
	}
	glog.Infof("Successfully created cinder volume %s", volumeId)
	cClient, err := p.cloud.NewBlockStorageV2()
	if err != nil {
		glog.Infof("failed to get cinder client: %v", err)
	} else {
		opt := volumeactions.InitializeConnectionOpts{
			Host:      "localhost",
			IP:        "127.0.0.1",
			Initiator: "com.example:www.test.com",
		}
		connectionInfo, err := volumeactions.InitializeConnection(cClient, volumeId, &opt).Extract()
		if err == nil {
			glog.Infof("connection info: %+v", connectionInfo)
		} else {
			glog.Infof("failed to initialize connection :%v", err)
		}
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn: p.identity,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				Cinder: &v1.CinderVolumeSource{
					VolumeID: volumeId,
				},
			},
		},
	}

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *cinderProvisioner) Delete(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[provisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{"identity annotation on PV does not match ours"}
	}
	// TODO when beta is removed, have to check kube version and pick v1/beta
	// accordingly: maybe the controller lib should offer a function for that
	return nil
}

var (
	master      = flag.String("master", "", "Master URL")
	kubeconfig  = flag.String("kubeconfig", "", "Absolute path to the kubeconfig")
	id          = flag.String("id", "", "Unique provisioner identity")
	cloudconfig = flag.String("cloudconfig", "", "Path to OpenStack config file")
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	if *cloudconfig == "" {
		glog.Fatalf("missing OpenStack config file")
	}

	var config *rest.Config
	var err error
	if *master != "" || *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags(*master, *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	prId := string(uuid.NewUUID())
	if *id != "" {
		prId = *id
	}
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	cinderProvisioner, err := newCinderProvisioner(clientset, prId, *cloudconfig)
	if err != nil {
		glog.Fatalf("Error creating Cinder provisioner: %v", err)
	}

	// Start the provision controller which will dynamically provision cinder
	// PVs
	pc := controller.NewProvisionController(
		clientset,
		provisionerName,
		cinderProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
