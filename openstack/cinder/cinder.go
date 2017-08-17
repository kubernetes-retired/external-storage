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

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"

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
	cinderVolumeId = "cinderVolumeId"
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

	vol, err := createCinderVolume(p.cloud, options)
	if err != nil {
		glog.Errorf("Failed to create volume")
		return nil, err
	}

	connection, err := vol.connect()
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to connect volume: %v", err)
		return nil, err
	}

	mapper, err := newVolumeMapperFromConnection(p, connection)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Unable to create volume mapper: %f" ,err)
		return nil, err
	}

	err = mapper.AuthSetup(options, connection)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to prepare volume auth: %v", err)
		return nil, err
	}

	pv, err := mapper.BuildPV(options, connection)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to build PV: %v", err)
		return nil, err
	}

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *cinderProvisioner) Delete(pv *v1.PersistentVolume) error {
	ann, ok := pv.Annotations[provisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{"identity annotation on PV does not match ours"}
	}
	// TODO when beta is removed, have to check kube version and pick v1/beta
	// accordingly: maybe the controller lib should offer a function for that

	mapper, err := newVolumeMapperFromPV(p, pv)
	if err != nil {
		glog.Errorf("Cannot create volume mapper from PV: %v", err)
		return err
	}
	volumeId := mapper.getCinderVolumeId()

	mapper.AuthTeardown(pv)

	vol := newCinderVolume(p.cloud, volumeId)
	err = vol.disconnect()
	if err != nil {
		glog.Errorf("Failed to disconnect volume: %f", err)
	}

	err = p.cloud.DeleteVolume(volumeId)
	if err != nil {
		glog.Errorf("Error deleting cinder volume: %v", err)
		return err
	}
	glog.Infof("Successfully deleted cinder volume %s", volumeId)

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
