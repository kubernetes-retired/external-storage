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

package collectors

import (
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/provisioner/pkg/deleter"
	"k8s.io/kube-state-metrics/pkg/collectors/testutils"
)

func newUint64Pointer(i uint64) *uint64 {
	return &i
}

func TestProcTableCollector(t *testing.T) {
	// Fixed metadata on type and help text. We prepend this to every expected
	// output so we only have to modify a single place when doing adjustments.
	const metadata = `
		# HELP local_volume_provisioner_proctable_succeeded Number of succeeded operations in proctable.
		# TYPE local_volume_provisioner_proctable_succeeded gauge
		# HELP local_volume_provisioner_proctable_failed Number of failed operations in proctable.
		# TYPE local_volume_provisioner_proctable_failed gauge
		# HELP local_volume_provisioner_proctable_running Number of running operations in proctable.
		# TYPE local_volume_provisioner_proctable_running gauge
	`

	var (
		want = metadata + `
			local_volume_provisioner_proctable_running 2
			local_volume_provisioner_proctable_succeeded 2
			local_volume_provisioner_proctable_failed 1
			`

		metrics = []string{
			"local_volume_provisioner_proctable_running",
			"local_volume_provisioner_proctable_succeeded",
			"local_volume_provisioner_proctable_failed",
		}
	)

	fakeProcTable := deleter.NewFakeProcTable()
	fakeProcTable.MarkRunning("pv1")
	fakeProcTable.MarkRunning("pv2")
	fakeProcTable.MarkRunning("pv3")
	fakeProcTable.MarkRunning("pv4")
	fakeProcTable.MarkRunning("pv5")
	fakeProcTable.MarkSucceeded("pv1")
	fakeProcTable.MarkFailed("pv2")
	fakeProcTable.MarkSucceeded("pv3")
	if err := testutils.GatherAndCompare(&procTableCollector{procTable: fakeProcTable}, want, metrics); err != nil {
		t.Errorf("unexpected collecting result:\n%s", err)
	}
}
