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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/util/clock"
)

func TestPodCache_Basic(t *testing.T) {
	fakeTime := time.Date(2017, time.May, 8, 0, 0, 0, 0, time.UTC)
	ttl := time.Minute

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "foo"}, Spec: v1.PodSpec{NodeName: "foo"}}
	c := NewFakePodCache(ttl, clock.NewFakeClock(fakeTime))
	c.AddPod(pod)

	pods := c.ListAllPods()
	if len(pods) != 1 {
		t.Errorf("Unexpected number of pods, expected 1, got %v", len(pods))
	}

	if pods[0].Name != "foo" {
		t.Errorf("Unexpected pod, expected pod name foo, got %v", pods[0].Name)
	}

	c.DeletePod(pod)
	pods = c.ListAllPods()
	if len(pods) != 0 {
		t.Errorf("Unexpected number of pods, expected 0, got %v", len(pods))
	}
}

func TestPodCache_TTL(t *testing.T) {
	fakeTime := time.Date(2017, time.May, 8, 0, 0, 0, 0, time.UTC)
	ttl := 30 * time.Second
	exactlyOnTTL := fakeTime.Add(-ttl)
	expiredTime := fakeTime.Add(-(ttl + 1))

	fakeClock := clock.NewFakeClock(exactlyOnTTL)
	c := NewFakePodCache(ttl, fakeClock)

	// pod1 exactly reaches ttl, do not consider it as expired.
	pod1 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "foo"}, Spec: v1.PodSpec{NodeName: "foo"}}
	c.AddPod(pod1)

	// pod2 is expired
	fakeClock.SetTime(expiredTime)
	pod2 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "bar"}, Spec: v1.PodSpec{NodeName: "bar"}}
	c.AddPod(pod2)

	fakeClock.SetTime(fakeTime)
	pods := c.ListAllPods()
	if len(pods) != 2 {
		t.Errorf("Unexpected number of pods, expected 2, got %v", len(pods))
	}

	pods = c.ListExpiredPods()
	if len(pods) != 1 {
		t.Errorf("Unexpected number of pods, expected 1, got %v", len(pods))
	}

	if pods[0].Name != "bar" {
		t.Errorf("Unexpected pod, expected pod name bar, got %v", pods[0].Name)
	}

	pods = c.ListAllPods()
	if len(pods) != 1 {
		t.Errorf("Unexpected number of pods, expected 1, got %v", len(pods))
	}

	if pods[0].Name != "foo" {
		t.Errorf("Unexpected pod, expected pod name foo, got %v", pods[0].Name)
	}
}
