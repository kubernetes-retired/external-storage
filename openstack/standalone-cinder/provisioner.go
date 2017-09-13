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
	"github.com/gophercloud/gophercloud"
	"github.com/kubernetes-incubator/external-storage/lib/controller"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	provisionerName  = "openstack.org/standalone-cinder"
	provisionerIDAnn = "standaloneCinderProvisionerIdentity"
	cinderVolumeID = "cinderVolumeId"
)

type cinderProvisioner struct {
	// Openstack cinder client
	volumeService *gophercloud.ServiceClient

	// Kubernetes Client. Use to create secret
	client kubernetes.Interface
	// Identity of this cinderProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string
}

func newCinderProvisioner(client kubernetes.Interface, id, configFilePath string) (controller.Provisioner, error) {
	volumeService, err := getVolumeService(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume service: %v", err)
	}

	return &cinderProvisioner{
		volumeService: volumeService,
		client:        client,
		identity:      id,
	}, nil
}

var _ controller.Provisioner = &cinderProvisioner{}

type provisionCtx struct {
	p          *cinderProvisioner
	options    controller.VolumeOptions
	connection volumeConnection
}

type deleteCtx struct {
	p  *cinderProvisioner
	pv *v1.PersistentVolume
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *cinderProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	volumeID, err := createCinderVolume(p, options)
	if err != nil {
		glog.Errorf("Failed to create volume")
		return nil, err
	}

	connection, err := connectCinderVolume(p, volumeID)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to connect volume: %v", err)
		return nil, err
	}

	mapper, err := newVolumeMapperFromConnection(connection)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Unable to create volume mapper: %f", err)
		return nil, err
	}

	ctx := provisionCtx{p, options, connection}
	err = mapper.AuthSetup(ctx)
	if err != nil {
		// TODO: Create placeholder PV?
		glog.Errorf("Failed to prepare volume auth: %v", err)
		return nil, err
	}

	pv, err := buildPV(mapper, ctx, volumeID)
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
		return &controller.IgnoredError{
			Reason: "identity annotation on PV does not match ours",
		}
	}
	// TODO when beta is removed, have to check kube version and pick v1/beta
	// accordingly: maybe the controller lib should offer a function for that

	volumeID, ok := pv.Annotations[cinderVolumeID]
	if !ok {
		return errors.New("cinder volume id annotation not found on PV")
	}

	ctx := deleteCtx{p, pv}
	mapper, err := newVolumeMapperFromPV(ctx)
	if err != nil {
		return err
	}

	mapper.AuthTeardown(ctx)

	err = disconnectCinderVolume(p, volumeID)
	if err != nil {
		return err
	}

	err = deleteCinderVolume(p, volumeID)
	if err != nil {
		return err
	}

	glog.Infof("Successfully deleted cinder volume %s", volumeID)
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
	prID := string(uuid.NewUUID())
	if *id != "" {
		prID = *id
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
	cinderProvisioner, err := newCinderProvisioner(clientset, prID, *cloudconfig)
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
