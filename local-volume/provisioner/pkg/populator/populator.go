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

package populator

import (
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/common"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	kcache "k8s.io/client-go/tools/cache"
)

// The Populator uses an Informer to populate the VolumeCache.
type Populator struct {
	*common.RuntimeConfig
}

// NewPopulator returns a Populator object to update the PV cache
func NewPopulator(config *common.RuntimeConfig) *Populator {
	return &Populator{RuntimeConfig: config}
}

// Start launches the PV informer
func (p *Populator) Start() {
	_, controller := kcache.NewInformer(
		&kcache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				pvs, err := p.Client.Core().PersistentVolumes().List(options)
				return pvs, err
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				// TODO: can we just watch for changes on the phase field?
				w, err := p.Client.Core().PersistentVolumes().Watch(options)
				return w, err
			},
		},
		&v1.PersistentVolume{},
		0,
		kcache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pv, ok := obj.(*v1.PersistentVolume)
				if !ok {
					glog.Errorf("Added object is not a v1.PersistentVolume type")
				}
				p.handlePVUpdate(pv)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				newPV, ok := newObj.(*v1.PersistentVolume)
				if !ok {
					glog.Errorf("Updated object is not a v1.PersistentVolume type")
				}
				p.handlePVUpdate(newPV)
			},
			DeleteFunc: func(obj interface{}) {
				pv, ok := obj.(*v1.PersistentVolume)
				if !ok {
					glog.Errorf("Added object is not a v1.PersistentVolume type")
				}
				p.handlePVDelete(pv)
			},
		},
	)

	glog.Infof("Starting Informer controller")
	// Controller never stops
	go controller.Run(make(chan struct{}))

	glog.Infof("Waiting for Informer initial sync")
	wait.Poll(time.Second, 5*time.Minute, func() (bool, error) {
		return controller.HasSynced(), nil
	})
	if !controller.HasSynced() {
		glog.Errorf("Informer controller initial sync timeout")
		os.Exit(1)
	}
}

func (p *Populator) handlePVUpdate(pv *v1.PersistentVolume) {
	_, exists := p.Cache.GetPV(pv.Name)
	if exists {
		p.Cache.UpdatePV(pv)
	} else {
		if pv.Annotations != nil {
			provisionerTag, found := pv.Annotations[common.AnnProvisionedBy]
			if !found {
				return
			}
			if provisionerTag == p.Tag {
				// This PV was created by this provisioner
				p.Cache.AddPV(pv)
			}
		}
	}
}

func (p *Populator) handlePVDelete(pv *v1.PersistentVolume) {
	_, exists := p.Cache.GetPV(pv.Name)
	if exists {
		// Don't do cleanup, just delete from cache
		p.Cache.DeletePV(pv.Name)
	}
}
