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

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/ceph/rbd/pkg/provision"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	master     = flag.String("master", "", "Master URL")
	kubeconfig = flag.String("kubeconfig", "", "Absolute path to the kubeconfig")
	id         = flag.String("id", "", "Unique provisioner identity")
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
	glog.Infof("Creating RBD provisioner with identity: %s", prID)
	rbdProvisioner := provision.NewRBDProvisioner(clientset, prID)

	// Start the provision controller which will dynamically provision rbd
	// PVs
	pc := controller.NewProvisionController(
		clientset,
		provision.ProvisionerName,
		rbdProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
