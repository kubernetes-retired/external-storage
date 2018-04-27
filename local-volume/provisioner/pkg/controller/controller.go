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

package controller

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/cache"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/discovery"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/populator"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/provisioningmanager"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/util"

	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/util/mount"
)

// StartLocalController starts the sync loop for the local PV discovery and deleter
func StartLocalController(client *kubernetes.Clientset, config *common.UserConfig) {
	glog.Info("Initializing volume cache\n")

	provisionerTag := fmt.Sprintf("local-volume-provisioner-%v-%v", config.Node.Name, config.Node.UID)

	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(client.CoreV1().RESTClient()).Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: provisionerTag})

	runtimeConfig := &common.RuntimeConfig{
		UserConfig:     config,
		Cache:          cache.NewVolumeCache(),
		VolUtil:        util.NewVolumeUtil(),
		APIUtil:        util.NewAPIUtil(client),
		Client:         client,
		Tag:            provisionerTag,
		Recorder:       recorder,
		Mounter:        mount.New("" /* default mount path */),
		ProvisionQueue: workqueue.NewNamed("claimsToProvision"),
	}

	populator := populator.NewPopulator(runtimeConfig)
	populator.Start()

	var jobController deleter.JobController
	var err error
	if runtimeConfig.UseJobForCleaning {
		stopCh := make(chan struct{})

		labels := map[string]string{common.NodeNameLabel: config.Node.Name}
		jobController, err = deleter.NewJobController(client, runtimeConfig.Namespace, labels, runtimeConfig)
		if err != nil {
			glog.Fatalf("Error starting jobController: %v", err)
		}
		go jobController.Run(stopCh)
		glog.Infof("Enabling Jobs based cleaning.")
	}
	cleanupTracker := &deleter.CleanupStatusTracker{ProcTable: deleter.NewProcTable(), JobController: jobController}

	discoverer, err := discovery.NewDiscoverer(runtimeConfig, cleanupTracker)
	if err != nil {
		glog.Fatalf("Error starting discoverer: %v", err)
	}

	dynamicProvisioningManager, err := provisioningmanager.NewManager(runtimeConfig)
	if err != nil {
		glog.Fatalf("Error starting dynamic provisioning manager: %v", err)
	}
	dynamicProvisioningManager.Start()

	deleter := deleter.NewDeleter(runtimeConfig, cleanupTracker)

	glog.Info("Controller started\n")
	for {
		deleter.DeletePVs()
		discoverer.DiscoverLocalVolumes()
		time.Sleep(10 * time.Second)
	}
}
