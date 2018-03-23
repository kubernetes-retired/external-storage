/*
Copyright 2018 The Kubernetes Authors.

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
	"flag"
	"fmt"
	"os"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/apiversions"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	sharedfilesystems "github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	provisionerName                    = "externalstorage.k8s.io/manila"
	minimumSupportedManilaMicroversion = "2.21"
)

type manilaProvisioner struct {
}

var (
	_          controller.Provisioner = &manilaProvisioner{}
	kubeconfig                        = flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := buildConfig(*kubeconfig)
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

	// Start the provision controller which will dynamically provision Manila PVs
	provisioner := controller.NewProvisionController(
		clientset,
		provisionerName,
		&manilaProvisioner{},
		serverVersion.GitVersion,
	)

	provisioner.Run(wait.NeverStop)
}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// Provision creates a new Manila share and returns a PV object representing it.
func (p *manilaProvisioner) Provision(pvc controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if pvc.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	client := createManilaV2Client()

	var err error
	var createdShare shares.Share
	var createReq shares.CreateOpts
	if createReq, err = sharedfilesystems.PrepareCreateRequest(pvc); err != nil {
		return nil, fmt.Errorf("failed to create Create Request: %v", err)
	}
	glog.V(4).Infof("successfully created a share Create Request: %v", createReq)
	var createReqResponse *shares.Share
	if createReqResponse, err = shares.Create(client, createReq).Extract(); err != nil {
		return nil, fmt.Errorf("failed to create a share: %v", err)
	}
	glog.V(3).Infof("successfully created a share: (%v)", createReqResponse)
	createdShare = *createReqResponse
	if err = sharedfilesystems.WaitTillAvailable(client, createdShare.ID); err != nil {
		errMsg := fmt.Errorf("waiting for the share %q to become created failed: %v", createdShare.ID, err)
		glog.Errorf("%v", errMsg)
		if resultingErr := deleteShare(client, createdShare.ID); resultingErr != nil {
			return nil, resultingErr
		}
		return nil, errMsg
	}
	glog.V(4).Infof("the share %q is now in state created", createdShare.ID)

	grantAccessReq := shares.GrantAccessOpts{
		AccessType:  "ip",
		AccessTo:    "0.0.0.0/0",
		AccessLevel: "rw",
	}
	var grantAccessReqResponse *shares.AccessRight
	if grantAccessReqResponse, err = shares.GrantAccess(client, createdShare.ID, grantAccessReq).Extract(); err != nil {
		errMsg := fmt.Errorf("failed to grant access to the share %q: %v", createdShare.ID, err)
		glog.Errorf("%v", errMsg)
		if resultingErr := deleteShare(client, createdShare.ID); resultingErr != nil {
			return nil, resultingErr
		}
		return nil, errMsg
	}
	glog.V(4).Infof("granted access to the share %q: (%v)", createdShare.ID, grantAccessReqResponse)

	var chosenLocation shares.ExportLocation
	var getExportLocationsReqResponse []shares.ExportLocation
	if getExportLocationsReqResponse, err = shares.GetExportLocations(client, createdShare.ID).Extract(); err != nil {
		errMsg := fmt.Errorf("failed to get export locations for the share %q: %v", createdShare.ID, err)
		glog.Errorf("%v", errMsg)
		if resultingErr := deleteShare(client, createdShare.ID); resultingErr != nil {
			return nil, resultingErr
		}
		return nil, errMsg
	}
	glog.V(4).Infof("got export locations for the share %q: (%v)", createdShare.ID, getExportLocationsReqResponse)
	if chosenLocation, err = sharedfilesystems.ChooseExportLocation(getExportLocationsReqResponse); err != nil {
		errMsg := fmt.Errorf("failed to choose an export location for the share %q: %q", createdShare.ID, err.Error())
		fmt.Printf("%v", errMsg)
		if resultingErr := deleteShare(client, createdShare.ID); resultingErr != nil {
			return nil, resultingErr
		}
		return nil, errMsg
	}
	glog.V(4).Infof("selected export location for the share %q is: (%v)", createdShare.ID, chosenLocation)
	pv, err := sharedfilesystems.FillInPV(pvc, createdShare, chosenLocation)
	if err != nil {
		errMsg := fmt.Errorf("failed to fill in PV for the share %q: %q", createdShare.ID, err.Error())
		glog.Errorf("%v", errMsg)
		if resultingErr := deleteShare(client, createdShare.ID); resultingErr != nil {
			return nil, resultingErr
		}
		return nil, errMsg
	}
	glog.V(4).Infof("resulting PV for the share %q: (%v)", createdShare.ID, pv)

	return pv, nil
}

// Delete deletes the share the volume is associated with
func (p *manilaProvisioner) Delete(volume *v1.PersistentVolume) error {
	client := createManilaV2Client()
	shareID, err := sharedfilesystems.GetShareIDfromPV(volume)
	if err != nil {
		glog.Errorf("%q", err.Error())
		return err
	}
	return deleteShare(client, shareID)
}

func deleteShare(client *gophercloud.ServiceClient, shareID string) error {
	deleteResult := shares.Delete(client, shareID)
	glog.V(4).Infof("share %q delete result: (%v)", shareID, deleteResult)
	if deleteResult.Err != nil {
		errMsg := fmt.Sprintf("failed to delete share %q", shareID)
		glog.Errorf("%q", errMsg)
		return fmt.Errorf("%q", errMsg)
	}
	return nil
}

func createManilaV2Client() *gophercloud.ServiceClient {
	regionName := os.Getenv("OS_REGION_NAME")
	authOpts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		glog.Fatalf("%v", err)
	}
	glog.V(1).Infof("successfully read options from environment variables: OS_AUTH_URL(%q), OS_USERNAME/OS_USERID(%q/%q), OS_TENANT_NAME/OS_TENANT_ID(%q,%q), OS_DOMAIN_NAME/OS_DOMAIN_ID(%q,%q)", authOpts.IdentityEndpoint, authOpts.Username, authOpts.UserID)
	provider, err := openstack.AuthenticatedClient(authOpts)
	if err != nil {
		glog.Fatalf("authentication failed: %v", err)
	}
	glog.V(4).Infof("successfully created provider client: (%v)", provider)
	client, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{Region: regionName})
	if err != nil {
		glog.Fatalf("failed to create Manila v2 client: %v", err)
	}
	client.Microversion = minimumSupportedManilaMicroversion
	serverVer, err := apiversions.Get(client, "v2").Extract()
	if err != nil {
		glog.Fatalf("failed to get Manila v2 API min/max microversions: %v", err)
	}
	glog.V(4).Infof("received server's microvesion data structure: (%v)", serverVer)
	glog.V(3).Infof("server's min microversion is: %q, max microversion is: %q", serverVer.MinVersion, serverVer.Version)
	if err = sharedfilesystems.ValidMicroversion(serverVer.MinVersion); err != nil {
		glog.Fatalf("server's minimum microversion is invalid: (%v)", serverVer.MinVersion)
	}
	if err = sharedfilesystems.ValidMicroversion(serverVer.Version); err != nil {
		glog.Fatalf("server's maximum microversion is invalid: (%v)", serverVer.Version)
	}
	clientMajor, clientMinor := sharedfilesystems.SplitMicroversion(client.Microversion)
	minMajor, minMinor := sharedfilesystems.SplitMicroversion(serverVer.MinVersion)
	if clientMajor < minMajor || (clientMajor == minMajor && clientMinor < minMinor) {
		glog.Fatalf("client microversion (%q) is smaller than the server's minimum microversion (%q)", client.Microversion, serverVer.MinVersion)
	}
	maxMajor, maxMinor := sharedfilesystems.SplitMicroversion(serverVer.Version)
	if maxMajor < clientMajor || (maxMajor == clientMajor && maxMinor < clientMinor) {
		glog.Fatalf("client microversion (%q) is bigger than the server's maximum microversion (%q)", client.Microversion, serverVer.Version)
	}
	glog.V(4).Infof("successfully created Manila v2 client: (%v)", client)
	return client
}
