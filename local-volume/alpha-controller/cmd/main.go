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
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/alpha-controller/pkg/controller"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var kubeconfig string
	var master string
	var threshold time.Duration

	flag.Set("logtostderr", "true")

	flag.StringVar(&kubeconfig, "kubeconfig", "", "The absolute path to the kubeconfig file.")
	flag.StringVar(&master, "master", "", "If provided, master url will override server address in kubeconfig file.")
	flag.DurationVar(&threshold, "threshold", 30*time.Second, "Unbind PV/PVC if pod with local volume request stays in pending status for this long.")
	flag.Parse()

	// Creates client config from kubeconfig and override master's address if
	// provided. If none of the flags are provided, fall back to in-cluster.
	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		glog.Fatal(err)
	}

	// Creates clientset.
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatal(err)
	}

	// Creates controller and start sync loop.
	stopCh := make(chan struct{})
	defer close(stopCh)

	ctrl := controller.NewController(clientset, threshold)
	go ctrl.Run(stopCh)

	select {}
}
