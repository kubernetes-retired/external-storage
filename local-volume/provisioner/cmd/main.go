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
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/controller"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var provisionerConfig common.ProvisionerConfiguration

func init() {
	provisionerConfig = common.ProvisionerConfiguration{
		StorageClassConfig: make(map[string]common.MountConfig),
	}
	if err := common.LoadProvisionerConfigs(&provisionerConfig); err != nil {
		glog.Fatalf("Error parsing Provisioner's configuration: %#v. Exiting...\n", err)
	}
	glog.Infof("Configuration parsing has been completed, ready to run...")
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	nodeName := os.Getenv("MY_NODE_NAME")
	if nodeName == "" {
		glog.Fatalf("MY_NODE_NAME environment variable not set\n")
	}

	client := common.SetupClient()
	node := getNode(client, nodeName)

	glog.Info("Starting controller\n")
	controller.StartLocalController(client, &common.UserConfig{
		Node:            node,
		DiscoveryMap:    provisionerConfig.StorageClassConfig,
		NodeLabelsForPV: provisionerConfig.NodeLabelsForPV,
	})
}

func getNode(client *kubernetes.Clientset, name string) *v1.Node {
	node, err := client.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("Could not get node information: %v", err)
	}
	return node
}
