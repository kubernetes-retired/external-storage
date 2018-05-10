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

package metrics

import (
	"testing"

	esUtil "github.com/kubernetes-incubator/external-storage/lib/util"
)

func TestCapacityBreakDown(t *testing.T) {
	testcases := []struct {
		capacityBytes  int64
		expectedString string
	}{
		{
			0,
			"0G",
		},
		{
			1,
			"500G",
		},
		{
			500 * esUtil.GiB,
			"500G",
		},
		{
			500*esUtil.GiB + 1,
			"1000G",
		},
		{
			1000*esUtil.GiB + 1,
			"1500G",
		},
	}

	for _, v := range testcases {
		got := CapacityBreakDown(v.capacityBytes)
		if got != v.expectedString {
			t.Errorf("got %s, expected: %s", got, v.expectedString)
		}
	}
}
