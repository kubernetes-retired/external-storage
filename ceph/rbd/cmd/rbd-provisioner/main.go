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

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/ceph/rbd/pkg/provision"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	master      = flag.String("master", "", "Master URL")
	kubeconfig  = flag.String("kubeconfig", "", "Absolute path to the kubeconfig")
	id          = flag.String("id", "", "Unique provisioner identity")
	metricsPort = flag.Int("metrics-port", 0, "The port of the metrics server (set to non-zero to enable)")
)

const (
	provisionerNameKey = "PROVISIONER_NAME"
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	var config *rest.Config
	var err error
	if *master != "" || *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags(*master, *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	prName := provision.ProvisionerName
	prNameFromEnv := os.Getenv(provisionerNameKey)
	if prNameFromEnv != "" {
		prName = prNameFromEnv
	}

	// By default, we use provisioner name as provisioner identity.
	// User may specify their own identity with `-id` flag to distinguish each
	// others, if they deploy more than one RBD provisioners under same provisioner name.
	prID := prName
	if *id != "" {
		prID = *id
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	glog.Infof("Creating RBD provisioner %s with identity: %s", prName, prID)
	rbdProvisioner := provision.NewRBDProvisioner(clientset, prID)

	// Start the provision controller which will dynamically provision rbd
	// PVs
	pc := controller.NewProvisionController(
		clientset,
		prName,
		rbdProvisioner,
		serverVersion.GitVersion,
		controller.MetricsPort(int32(*metricsPort)),
	)

	pc.Run(wait.NeverStop)
}
