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

	"github.com/cloudflare/cfssl/log"
	"github.com/kubernetes-incubator/external-storage/local-volume/alpha-controller/pkg/cache"

	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/v1"
	kubecache "k8s.io/client-go/tools/cache"
)

const (
	PodResource    = "pods"
	PodStatusField = "status.phase"

	PersistentVolumeResource      = "persistentvolumes"
	PersistentVolumeClaimResource = "persistentvolumeclaims"
)

// Controller looks for all pods that are unschedulable for a long
// time due to local volume binding problem.
type Controller struct {
	clientset   *kubernetes.Clientset
	podIndexer  kubecache.Indexer
	podInformer kubecache.Controller

	volumeLister   corelisters.PersistentVolumeLister
	volumeInformer kubecache.Controller

	claimLister   corelisters.PersistentVolumeClaimLister
	claimInformer kubecache.Controller

	threshold       time.Duration
	expirationCache *cache.PodCache
	inspectionCache *cache.PodCache
}

// NewController creates a new controller to handle pending pods.
func NewController(clientset *kubernetes.Clientset, threshold time.Duration) *Controller {
	c := &Controller{
		clientset: clientset,
		threshold: threshold,
	}

	// Creates pod list watcher, we are only interested in pods that are in pending status.
	fieldSelector := fields.Set{PodStatusField: string(v1.PodPending)}.AsSelector()
	podListWatcher := kubecache.NewListWatchFromClient(clientset.Core().RESTClient(), PodResource, v1.NamespaceAll, fieldSelector)
	podIndexer, podInformer := kubecache.NewIndexerInformer(podListWatcher, &v1.Pod{}, 0, kubecache.ResourceEventHandlerFuncs{
		// Note we only handle Add and Delete events; update will be handled lazily
		// during cache processing. This simplifies controller logic as update can be
		// quite subtle to deal with. For example, if old pod is pending with local
		// volume request but is then updated to not using local volume, we then must
		// remove the pod from expirationCache if it exists.
		AddFunc:    func(obj interface{}) { c.handlePodAdd(obj.(*v1.Pod)) },
		DeleteFunc: func(obj interface{}) { c.handlePodDelete(obj.(*v1.Pod)) },
	}, kubecache.Indexers{})
	c.podIndexer = podIndexer
	c.podInformer = podInformer

	// Creates volume indexer, informer, lister.
	volumeListWatcher := kubecache.NewListWatchFromClient(clientset.Core().RESTClient(), PersistentVolumeResource, v1.NamespaceAll, fields.Everything())
	volumeIndexer, volumeInformer := kubecache.NewIndexerInformer(volumeListWatcher, &v1.PersistentVolume{}, 0, kubecache.ResourceEventHandlerFuncs{}, kubecache.Indexers{})
	c.volumeLister = corelisters.NewPersistentVolumeLister(volumeIndexer)
	c.volumeInformer = volumeInformer

	// Creates claim indexer, informer, lister.
	claimListWatcher := kubecache.NewListWatchFromClient(clientset.Core().RESTClient(), PersistentVolumeClaimResource, v1.NamespaceAll, fields.Everything())
	claimIndexer, claimInformer := kubecache.NewIndexerInformer(claimListWatcher, &v1.PersistentVolumeClaim{}, 0, kubecache.ResourceEventHandlerFuncs{}, kubecache.Indexers{})
	c.claimLister = corelisters.NewPersistentVolumeClaimLister(claimIndexer)
	c.claimInformer = claimInformer

	// Creates inspection cache and timed cache. Two caches are need instead of
	// one to handle cases when Pod is pending for different reasons, i.e. pod
	// is pending before due to claim not found, but after a while, user creates
	// the claim but the PV/PVC binding is wrong. In base cases, pod stays in
	// pending state, but we need to deal with them differently.
	c.inspectionCache = cache.NewPodCache(threshold)
	c.expirationCache = cache.NewPodCache(threshold)

	return c
}

// Run starts informers and controller loop.
func (c *Controller) Run(stopCh chan struct{}) {
	defer runtime.HandleCrash()

	log.Info("Starting localvolume controller")
	defer log.Info("Shutting down endpoint controller")

	go c.volumeInformer.Run(stopCh)
	go c.claimInformer.Run(stopCh)

	// Wait for all secondary caches to be synced, before processing pods.
	if !kubecache.WaitForCacheSync(stopCh, c.volumeInformer.HasSynced, c.claimInformer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	go c.podInformer.Run(stopCh)
	go wait.Until(c.processSuspiciousPod, c.threshold, wait.NeverStop)

	<-stopCh
	log.Info("Stopping Pod controller")
}

// handlePodAdd handles onAdd event from informer.
func (c *Controller) handlePodAdd(pod *v1.Pod) {
	_, lvRequest, err := c.withLocalVolumeRequest(pod)

	// Pod is pending due to:
	//  - claim not found;
	//  - claim not bound;
	//  - possibly other volume related errors
	// In this case, we put pod into an inspection cache to keep inspecting the
	// pod in case pv/pvc are created later but pod still can't be scheduled (and
	// we won't get notified because pod is still pending, but now for different
	// reason).
	if err != nil {
		log.Infof("PodAdd: error checking local volume request, add pod %v/%v into inspection cache\n", pod.Namespace, pod.Name)
		c.inspectionCache.AddPod(pod)
		return
	}

	// Pod doesn't request any local volume, skip.
	if !lvRequest {
		log.Infof("PodAdd: ignore pod %v/%v with no local volume request\n", pod.Namespace, pod.Name)
		return
	}

	// Pod is pending and claims a local volume, start counting down.
	log.Infof("PodAdd: pod %v/%v is pending and has local volume request, put into expiration cache\n", pod.Namespace, pod.Name)
	c.expirationCache.AddPod(pod)
}

// handlePodDelete handles onDelete event from informer.
func (c *Controller) handlePodDelete(pod *v1.Pod) {
	_, lvRequest, err := c.withLocalVolumeRequest(pod)

	if err != nil {
		log.Infof("PodDelete: error checking local volume request, remove pod %v/%v from inspection cache\n", pod.Namespace, pod.Name)
		c.inspectionCache.DeletePod(pod)
		return
	}

	if !lvRequest {
		log.Infof("PodDelete: ignore pod %v/%v with no local volume request\n", pod.Namespace, pod.Name)
		return
	}

	log.Infof("PodDelete: pod %v/%v is pending and has local volume request, remove from expiration cache\n", pod.Namespace, pod.Name)
	c.expirationCache.DeletePod(pod)
}

// processSuspiciousPod hanldes all pods likely to subject to PV/PVC binding issue.
func (c *Controller) processSuspiciousPod() {
	for _, pod := range c.expirationCache.ListExpiredPods() {
		volumes, lvRequest, err := c.withLocalVolumeRequest(pod)
		if err != nil {
			log.Infof("expirationCache: error checking pod %v/%v local volume request\n", pod.Namespace, pod.Name)
			c.inspectionCache.AddPod(pod)
			continue
		}
		if !lvRequest {
			log.Infof("expirationCache: remove pod %v/%v which is updated to not use local volume\n", pod.Namespace, pod.Name)
			continue
		}
		log.Infof("expirationCache: unbind PV/PVC for pod %v/%v\n", pod.Namespace, pod.Name)
		if err := c.unbindVolme(volumes); err != nil {
			log.Infof("error unbind volume %v\n", err)
			c.expirationCache.AddPod(pod)
		}
	}

	for _, pod := range c.inspectionCache.ListExpiredPods() {
		_, lvRequest, err := c.withLocalVolumeRequest(pod)
		if err != nil {
			log.Infof("inspectionCache: still error processing pod %v/%v\n", pod.Namespace, pod.Name)
			c.inspectionCache.AddPod(pod)
			continue
		}
		if !lvRequest {
			log.Infof("inspectionCache: remove pod %v/%v which is updated to not use local volume\n", pod.Namespace, pod.Name)
			continue
		}
		// Pod is pending before due to error checking PV/PVC, but now error has gone
		// and pod is still pending. Add the pod to expirationCache to check later.
		log.Infof("inspectionCache: pod %v/%v is pending and has local volume request\n", pod.Namespace, pod.Name)
		c.expirationCache.AddPod(pod)
	}
}

// localvolume in an internal struct to keep volume and claim of a Pod.
type localvolume struct {
	volume *v1.PersistentVolume
	claim  *v1.PersistentVolumeClaim
}

// withLocalVolumeRequest returns true if a Pod has local volume request.
func (c *Controller) withLocalVolumeRequest(pod *v1.Pod) ([]*localvolume, bool, error) {
	if len(pod.Spec.Volumes) == 0 {
		return nil, false, nil
	}

	// A list of local volumes in Pod.
	volumes := []*localvolume{}

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claim, err := c.claimLister.PersistentVolumeClaims(pod.Namespace).Get(volume.PersistentVolumeClaim.ClaimName)
			if err != nil {
				log.Infof("couldn't find claim for pod %v/%v (volume %v)\n", pod.Namespace, pod.Name, volume.PersistentVolumeClaim.ClaimName)
				return nil, false, err
			}
			log.Infof("found claim %v for pod %v/%v's volume %v\n", claim.Name, pod.Namespace, pod.Name, volume.Name)

			claimedVolume, err := c.volumeLister.Get(claim.Spec.VolumeName)
			// claim is not bound yet, similar to claim not found, we put pod into cache.
			if err != nil {
				log.Infof("pvc claimed by pod %v/%v's volume %v is not bound\n", pod.Namespace, pod.Name, volume.Name)
				return nil, false, err
			}
			log.Infof("found volume object claimed by pod %v/%v's volume %v\n", pod.Namespace, pod.Name, volume.Name)

			// pod claimed a local persistent volume, put it into timed cache.
			if claimedVolume.Spec.Local != nil {
				volumes = append(volumes, &localvolume{claimedVolume, claim})
			}
		}
	}

	if len(volumes) != 0 {
		return volumes, true, nil
	}

	return nil, false, nil
}

// unbindVolme will unbind all local pv/pvc; this is not ideal, but is simpler
// to implement.
func (c *Controller) unbindVolme(volumes []*localvolume) error {
	for _, localvolume := range volumes {
		// Recreate persistent volume claim. The rest will be handled via static
		// provisioner, i.e. recreate persistent volume bound by this pvc.
		clone, err := api.Scheme.DeepCopy(localvolume.claim)
		if err != nil {
			return err
		}
		claim := clone.(*v1.PersistentVolumeClaim)
		err = c.clientset.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(claim.Name, &meta_v1.DeleteOptions{})
		if err != nil {
			return err
		}
		claim.ObjectMeta = meta_v1.ObjectMeta{Name: claim.Name, Namespace: claim.Namespace}
		claim.Status = v1.PersistentVolumeClaimStatus{}
		_, err = c.clientset.CoreV1().PersistentVolumeClaims(claim.Namespace).Create(claim)
		if err != nil {
			return err
		}
	}
	return nil
}
