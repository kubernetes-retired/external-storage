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

package cache

import (
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/util/clock"
)

// timestampedPod is a pod with timestamp.
type timestampedPod struct {
	pod       *v1.Pod
	timestamp time.Time
}

// PodCache is a cache store for pods. All pods are automatically time stamped
// on insert. The key is computed based on pod.Namespace and pod.Name; the value
// is a timestamped pod.
type PodCache struct {
	rwLock  sync.RWMutex
	pods    map[string]*timestampedPod
	clock   clock.Clock
	ttl     time.Duration
	keyFunc func(pod *v1.Pod) string
}

// NewPodCache returns a new PodCache.
func NewPodCache(ttl time.Duration) *PodCache {
	cache := &PodCache{
		rwLock: sync.RWMutex{},
		pods:   map[string]*timestampedPod{},
		clock:  clock.RealClock{},
		ttl:    ttl,
		keyFunc: func(pod *v1.Pod) string {
			return strings.Join([]string{pod.Namespace, pod.Name}, "/")
		},
	}

	return cache
}

// AddPod adds a pod to cache, with timestamp set to now.
func (c *PodCache) AddPod(pod *v1.Pod) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.pods[c.keyFunc(pod)] = &timestampedPod{
		pod:       pod,
		timestamp: c.clock.Now(),
	}
}

// DeletePod deletes a pod from the cache.
func (c *PodCache) DeletePod(pod *v1.Pod) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	delete(c.pods, c.keyFunc(pod))
}

// ListAllPods lists all pods in the cache, regardless of its time stamp.
func (c *PodCache) ListAllPods() []*v1.Pod {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	pods := []*v1.Pod{}
	for _, timestampedPod := range c.pods {
		pods = append(pods, timestampedPod.pod)
	}

	return pods
}

// ListExpiredPods lists all expired pods. It will delete the pod from cache.
func (c *PodCache) ListExpiredPods() []*v1.Pod {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	pods := []*v1.Pod{}
	for _, timestampedPod := range c.pods {
		if c.clock.Since(timestampedPod.timestamp) > c.ttl {
			delete(c.pods, c.keyFunc(timestampedPod.pod))
			pods = append(pods, timestampedPod.pod)
		}
	}

	return pods
}

func NewFakePodCache(ttl time.Duration, clock clock.Clock) *PodCache {
	cache := &PodCache{
		rwLock: sync.RWMutex{},
		pods:   map[string]*timestampedPod{},
		clock:  clock,
		ttl:    ttl,
		keyFunc: func(pod *v1.Pod) string {
			return strings.Join([]string{pod.Namespace, pod.Name}, "/")
		},
	}

	return cache
}
