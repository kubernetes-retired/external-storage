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
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/golang/glog"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/client"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider/providers/aws"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider/providers/gce"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider/providers/openstack"
	snapshotcontroller "github.com/kubernetes-incubator/external-storage/snapshot/pkg/controller/snapshot-controller"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume/awsebs"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume/cinder"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume/gcepd"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume/gluster"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/volume/hostpath"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
)

const (
	defaultSyncDuration time.Duration = 60 * time.Second
)

var (
	kubeconfig      = flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
	cloudProvider   = flag.String("cloudprovider", "", "aws|gce|openstack")
	cloudConfigFile = flag.String("cloudconfig", "", "Path to a Cloud config. Only required if cloudprovider is set.")
	volumePlugins   = make(map[string]volume.Plugin)
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")
	// Create the client config. Use kubeconfig if given, otherwise assume in-cluster.
	config, err := buildConfig(*kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	aeclientset, err := apiextensionsclient.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	// initialize CRD resource if it does not exist
	err = client.CreateCRD(aeclientset)
	if err != nil {
		panic(err)
	}

	// make a new config for our extension's API group, using the first config as a baseline
	snapshotClient, snapshotScheme, err := client.NewClient(config)
	if err != nil {
		panic(err)
	}

	// wait until CRD gets processed
	err = client.WaitForSnapshotResource(snapshotClient)
	if err != nil {
		panic(err)
	}
	// build volume plugins map
	buildVolumePlugins()

	// start controller on instances of our CRD
	glog.Infof("starting snapshot controller")
	ssController := snapshotcontroller.NewSnapshotController(snapshotClient, snapshotScheme, clientset, &volumePlugins, defaultSyncDuration)
	stopCh := make(chan struct{})

	go ssController.Run(stopCh)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	close(stopCh)

}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func buildVolumePlugins() {
	if len(*cloudProvider) != 0 {
		cloud, err := cloudprovider.InitCloudProvider(*cloudProvider, *cloudConfigFile)
		if err == nil && cloud != nil {
			if *cloudProvider == aws.ProviderName {
				awsPlugin := awsebs.RegisterPlugin()
				awsPlugin.Init(cloud)
				volumePlugins[awsebs.GetPluginName()] = awsPlugin
				glog.Info("Register cloudprovider aws")
			}
			if *cloudProvider == gce.ProviderName {
				gcePlugin := gcepd.RegisterPlugin()
				gcePlugin.Init(cloud)
				volumePlugins[gcepd.GetPluginName()] = gcePlugin
				glog.Info("Register cloudprovider %s", gcepd.GetPluginName())
			}
			if *cloudProvider == openstack.ProviderName {
				cinderPlugin := cinder.RegisterPlugin()
				cinderPlugin.Init(cloud)
				volumePlugins[cinder.GetPluginName()] = cinderPlugin
				glog.Info("Register cloudprovider %s", cinder.GetPluginName())
			}
		} else {
			glog.Warningf("failed to initialize cloudprovider: %v, supported cloudproviders are %#v", err, cloudprovider.CloudProviders())
		}
	}
	volumePlugins[gluster.GetPluginName()] = gluster.RegisterPlugin()
	volumePlugins[hostpath.GetPluginName()] = hostpath.RegisterPlugin()
}
