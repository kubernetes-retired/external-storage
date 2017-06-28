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

package fake

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
	metrics "k8s.io/metrics/pkg/apis/metrics"
)

// FakeNodeMetricses implements NodeMetricsInterface
type FakeNodeMetricses struct {
	Fake *FakeMetrics
}

var nodemetricsesResource = schema.GroupVersionResource{Group: "metrics", Version: "", Resource: "nodemetricses"}

func (c *FakeNodeMetricses) Get(name string, options v1.GetOptions) (result *metrics.NodeMetrics, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(nodemetricsesResource, name), &metrics.NodeMetrics{})
	if obj == nil {
		return nil, err
	}
	return obj.(*metrics.NodeMetrics), err
}

func (c *FakeNodeMetricses) List(opts v1.ListOptions) (result *metrics.NodeMetricsList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(nodemetricsesResource, opts), &metrics.NodeMetricsList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &metrics.NodeMetricsList{}
	for _, item := range obj.(*metrics.NodeMetricsList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested nodeMetricses.
func (c *FakeNodeMetricses) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(nodemetricsesResource, opts))
}
