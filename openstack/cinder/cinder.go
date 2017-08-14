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

	connInfo, err := p.connectVolume(volumeId)
	if err != nil {
		// TODO: remove the volume or fail permanently so we don't
		//       continue to recreate the volume each iteration.
		glog.Errorf("Failed to establish volume connection: %v", err)
		return nil, err
	}

	pv, err := p.buildPersistentVolume(options, connInfo)
	if err != nil {
		glog.Errorf("Failed to create PV for volume: %v", err)
		return nil, err
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

	// Remove the CHAP secret
	// TODO: Split this out into type-specific logic
	if volume.Spec.ISCSI != nil {
		secretName := volume.Spec.ISCSI.SecretRef.Name
		secretNamespace := volume.Spec.ClaimRef.Namespace
		err := p.client.CoreV1().Secrets(secretNamespace).Delete(secretName, nil)
		if err != nil {
			glog.Errorf("Failed to remove secret: %s, %v", secretName, err)
		} else{
			glog.Infof("Successfully deleted secret %s", secretName)
		}
	}

	volumeId, ok := volume.Annotations[cinderVolumeId]
	if !ok {
		return errors.New("cinder volume id annotation not found on PV")
	}
	p.disconnectVolume(volumeId)

	err := p.cloud.DeleteVolume(volumeId)
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
