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
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/controller"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/metrics/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	optListenAddress string
	optMetricsPath   string
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	flag.StringVar(&optListenAddress, "listen-address", ":8080", "address on which to expose metrics")
	flag.StringVar(&optMetricsPath, "metrics-path", "/metrics", "path under which to expose metrics")
	flag.Set("logtostderr", "true")
	flag.Parse()

	provisionerConfig := common.ProvisionerConfiguration{
		StorageClassConfig: make(map[string]common.MountConfig),
		MinResyncPeriod:    metav1.Duration{Duration: 5 * time.Minute},
	}
	if err := common.LoadProvisionerConfigs(common.ProvisionerConfigPath, &provisionerConfig); err != nil {
		glog.Fatalf("Error parsing Provisioner's configuration: %#v. Exiting...\n", err)
	}
	glog.Infof("Loaded configuration: %+v", provisionerConfig)
	glog.Infof("Ready to run...")

	nodeName := os.Getenv("MY_NODE_NAME")
	if nodeName == "" {
		glog.Fatalf("MY_NODE_NAME environment variable not set\n")
	}

	namespace := os.Getenv("MY_NAMESPACE")
	if namespace == "" {
		glog.Warningf("MY_NAMESPACE environment variable not set, will be set to default.\n")
		namespace = "default"
	}

	jobImage := os.Getenv("JOB_CONTAINER_IMAGE")
	if jobImage == "" {
		glog.Warningf("JOB_CONTAINER_IMAGE environment variable not set.\n")
	}

	client := common.SetupClient()
	node := getNode(client, nodeName)

	glog.Info("Starting controller\n")
	procTable := deleter.NewProcTable()
	go controller.StartLocalController(client, procTable, &common.UserConfig{
		Node:              node,
		DiscoveryMap:      provisionerConfig.StorageClassConfig,
		NodeLabelsForPV:   provisionerConfig.NodeLabelsForPV,
		UseAlphaAPI:       provisionerConfig.UseAlphaAPI,
		UseJobForCleaning: provisionerConfig.UseJobForCleaning,
		MinResyncPeriod:   provisionerConfig.MinResyncPeriod,
		UseNodeNameOnly:   provisionerConfig.UseNodeNameOnly,
		Namespace:         namespace,
		JobContainerImage: jobImage,
	})

	signal := make(chan int)
	go startChownProcessor(signal, provisionerConfig.StorageClassConfig)

	glog.Infof("Starting metrics server at %s\n", optListenAddress)
	prometheus.MustRegister([]prometheus.Collector{
		metrics.PersistentVolumeDiscoveryTotal,
		metrics.PersistentVolumeDiscoveryDurationSeconds,
		metrics.PersistentVolumeDeleteTotal,
		metrics.PersistentVolumeDeleteDurationSeconds,
		metrics.PersistentVolumeDeleteFailedTotal,
		metrics.APIServerRequestsTotal,
		metrics.APIServerRequestsFailedTotal,
		metrics.APIServerRequestsDurationSeconds,
		collectors.NewProcTableCollector(procTable),
	}...)
	http.Handle(optMetricsPath, promhttp.Handler())
	log.Fatal(http.ListenAndServe(optListenAddress, nil))
	signal <- 0 // signal shutdown
}

func getNode(client *kubernetes.Clientset, name string) *v1.Node {
	node, err := client.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("Could not get node information: %v", err)
	}
	return node
}

// exists returns whether the given file or directory exists or not
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

func chownR(path string, uid, gid int) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err == nil {
			err = os.Chown(name, uid, gid)
		}
		return err
	})
}

func startChownProcessor(signal <-chan int, storageClassConfig map[string]common.MountConfig) {
	glog.Infof("Starting chown-processor")
	running := true
	state := make(map[string]bool)
	for {
		if !running {
			break
		}
		completed := 0
		for _, config := range storageClassConfig {
			rc, ok := state[config.MountDir]
			if ok && rc {
				completed++
				continue
			}
			state[config.MountDir] = false
			if config.UID == "" || config.GID == "" {
				continue
			}
			uid, err := strconv.Atoi(config.UID)
			if err != nil {
				glog.Errorf("Cannot parse UID for [%s] mount: %s", config.MountDir, config.UID)
				continue
			}
			gid, err := strconv.Atoi(config.GID)
			if err != nil {
				glog.Errorf("Cannot parse GID for [%s] mount: %s", config.MountDir, config.GID)
				continue
			}
			exists, err := exists(config.MountDir)
			if !exists || err != nil {
				continue
			}
			if err = chownR(config.MountDir, uid, gid); err != nil {
				glog.Errorf("Failed to chown [%s] mount dir: %+v", config.MountDir, err)
				continue
			}
			glog.Infof("Mount dir chowned: %s %s:%s", config.MountDir, config.UID, config.GID)
			state[config.MountDir] = true
			completed++
		}
		select {
		case <-signal:
			running = false
		default:
			running = true
		}
		if completed == len(storageClassConfig) {
			glog.Infof("Finishing chown-processor: %d dirs processed", completed)
			running = false
		}
	}
}
