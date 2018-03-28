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
	"encoding/json"
	"flag"
	"fmt"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func cloneAndUpdatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error) {
	var affinity v1.NodeAffinity
	err := json.Unmarshal([]byte(pv.Annotations[v1.AlphaStorageNodeAffinityAnnotation]), &affinity)
	if err != nil {
		return nil, err
	}
	pvClone := pv.DeepCopy()
	delete(pvClone.Annotations, v1.AlphaStorageNodeAffinityAnnotation)
	pvClone.Spec.NodeAffinity = &v1.VolumeNodeAffinity{
		Required: affinity.RequiredDuringSchedulingIgnoredDuringExecution,
	}
	return pvClone, nil
}
func updateLocalPVAlphaAnn(client *kubernetes.Clientset, pv *v1.PersistentVolume) error {
	// if it is not local PV, return directly
	if pv.Spec.Local == nil {
		return nil
	}
	// check alpha node affinity annotation
	if len(pv.Annotations) > 0 && pv.Annotations[v1.AlphaStorageNodeAffinityAnnotation] != "" {
		pvClone, err := cloneAndUpdatePV(pv)
		if err != nil {
			return err
		}

		glog.Infof("Updating local PV(%s), node affinity is: %v", pv.Name, pv.Annotations[v1.AlphaStorageNodeAffinityAnnotation])
		_, err = client.CoreV1().PersistentVolumes().Update(pvClone)
		if err != nil {
			if errors.IsNotFound(err) {
				glog.Errorf("PV(%s) seems to have been deleted", pv.Name)
				return nil
			}
			return err
		}
	}
	return nil
}

func startSwitchingLocalPVAlphaAnn(client *kubernetes.Clientset) error {
	pvs, err := client.CoreV1().PersistentVolumes().List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("list PVs error: %v", err)
	}
	if len(pvs.Items) == 0 {
		glog.Infof("No PVs found, return directly")
		return nil
	}

	var updateFailed bool
	for _, pv := range pvs.Items {
		if err = updateLocalPVAlphaAnn(client, &pv); err != nil {
			glog.Errorf("update PV: %s error, %v", pv.Name, err)
			updateFailed = true
			// If err is TooManyRequests, return directly
			if errors.IsTooManyRequests(err) {
				return err
			}
		}
	}
	if updateFailed {
		return fmt.Errorf("updating local PVs error")
	}
	return nil
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	client := common.SetupClient()

	glog.Infof("Starting to update local PV node affinity to beta...")

	// do not retry here, let users set their Job configuration restart policy
	err := startSwitchingLocalPVAlphaAnn(client)
	if err != nil {
		glog.Fatalf("update local PVs alpha node affinity to beta error, %v", err)
	} else {
		glog.Infof("Update local PVs alpha node affinity to beta successfully.")
	}
}
