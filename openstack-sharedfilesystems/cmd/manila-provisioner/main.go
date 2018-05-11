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
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/sharebackends"
	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/shareoptions"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	minimumSupportedManilaMicroversion = "2.21"
)

type manilaProvisioner struct {
}

var (
	_               controller.Provisioner = &manilaProvisioner{}
	kubeconfig                             = flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
	provisionerName                        = flag.String("provisioner", "externalstorage.k8s.io/manila", "Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.")
	clientset       *kubernetes.Clientset
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

	clientset, err = kubernetes.NewForConfig(config)
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
		*provisionerName,
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
func (p *manilaProvisioner) Provision(volOptions controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if volOptions.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	// Initialization

	shareOptions, err := shareoptions.NewShareOptions(&volOptions)
	if err != nil {
		return nil, err
	}

	shareBackend, err := sharedfilesystems.GetShareBackend(shareOptions.Backend)
	if err != nil {
		return nil, err
	}

	client := createManilaV2Client()

	// Share creation

	share, err := p.createShare(&volOptions, shareOptions, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create a share: %v", err)
	}

	// Needed in deleteShare()
	sharedfilesystems.RegisterBackendForShare(shareOptions.Backend, share.ID)

	defer func() {
		// Delete the share if any of its setup operations fail
		if err != nil {
			if delErr := deleteShare(client, share.ID); delErr != nil {
				glog.Errorf("failed to delete share %s in a rollback procedure: %v", share.ID, delErr)
			}
		}
	}()

	if err = sharedfilesystems.WaitTillAvailable(client, share.ID); err != nil {
		return nil, fmt.Errorf("waiting for share %s to become created failed: %v", share.ID, err)
	}

	availableExportLocations, err := shares.GetExportLocations(client, share.ID).Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to get export locations for share %s: %v", share.ID, err)
	}

	chosenExportLocation, err := sharedfilesystems.ChooseExportLocation(availableExportLocations)
	if err != nil {
		fmt.Errorf("failed to choose an export location for share %s: %v", share.ID, err)
	}

	accessRight, err := shareBackend.GrantAccess(&sharebackends.GrantAccessArgs{share, client})
	if err != nil {
		return nil, fmt.Errorf("failed to grant access for share %s: %v", share.ID, err)
	}

	volSource, err := shareBackend.CreateSource(&sharebackends.CreateSourceArgs{
		Share:       share,
		Options:     shareOptions,
		Location:    &chosenExportLocation,
		Clientset:   clientset,
		AccessRight: accessRight,
	})
	if err != nil {
		return nil, fmt.Errorf("backend %s failed to create volume source for share %s: %v", shareBackend.Name(), share.ID, err)
	}

	return sharedfilesystems.CreatePersistentVolumeRequest(share, &volOptions, volSource), nil
}

func (p *manilaProvisioner) createShare(
	volOptions *controller.VolumeOptions,
	shareOptions *shareoptions.ShareOptions,
	client *gophercloud.ServiceClient,
) (*shares.Share, error) {
	req, err := sharedfilesystems.PrepareCreateRequest(volOptions, shareOptions)
	if err != nil {
		return nil, err
	}

	return shares.Create(client, *req).Extract()
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
	r := shares.Delete(client, shareID)
	if r.Err != nil {
		msg := fmt.Errorf("failed to delete share %s: %v", shareID, r.Err)
		glog.Errorln(msg)
		return msg
	}

	backendName, err := sharedfilesystems.GetBackendNameForShare(shareID)
	if err != nil {
		glog.Errorln(err)
		return err
	}

	shareBackend, err := sharedfilesystems.GetShareBackend(backendName)
	if err != nil {
		glog.Errorln(err)
		return err
	}

	if err = shareBackend.Release(&sharebackends.ReleaseArgs{shareID, clientset}); err != nil {
		glog.Errorln(err)
		return err
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
