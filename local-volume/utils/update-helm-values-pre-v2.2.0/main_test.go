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
	"testing"

	"github.com/kubernetes-incubator/external-storage/local-volume/utils/update-helm-values-pre-v2.2.0/pkg/chartutil"
)

func TestUpgrade(t *testing.T) {
	testcases := []struct {
		in       string
		engine   string
		expected string
	}{
		{
			`
common:
  useAlphaAPI: true
configmap:
  baremetal:
    storageClass:
    - fast-disks:
        blockCleanerCommand:
        - /scripts/shred.sh
        - "2"
        hostDir: /mnt/fast-disks
  configMapName: local-provisioner-test
  gcePre19:
    storageClass:
    - local-scsi:
        blockCleanerCommand:
        - /scripts/quick_reset.sh
        hostDir: /mnt/disks`,
			"baremetal",
			`
common:
  useAlphaAPI: true
  configMapName: local-provisioner-test
classes:
- name: fast-disks
  blockCleanerCommand:
  - /scripts/shred.sh
  - "2"
  hostDir: /mnt/fast-disks
daemonset:
  imagePullPolicy: Always`,
		},
		{
			`
common:
  useAlphaAPI: true
configmap:
  baremetal:
    storageClass:
    - fast-disks:
        blockCleanerCommand:
        - /scripts/shred.sh
        - "2"
        hostDir: /mnt/fast-disks
  configMapName: local-provisioner-test
  gcePre19:
    storageClass:
    - local-scsi:
        blockCleanerCommand:
        - /scripts/quick_reset.sh
        hostDir: /mnt/disks`,
			"gcePre19",
			`
common:
  useAlphaAPI: true
  configMapName: local-provisioner-test
classes:
- name: local-scsi
  blockCleanerCommand:
  - /scripts/quick_reset.sh
  hostDir: /mnt/disks
daemonset:
  imagePullPolicy: Always`,
		},
		{
			`
configmap:
  baremetal:
    storageClass:
    - fast-disks:
        blockCleanerCommand:
        - /scripts/shred.sh
        - "2"
        hostDir: /mnt/fast-disks
  configMapName: local-provisioner-test`,
			"baremetal",
			`
common:
  configMapName: local-provisioner-test
classes:
- name: fast-disks
  blockCleanerCommand:
  - /scripts/shred.sh
  - "2"
  hostDir: /mnt/fast-disks
daemonset:
  imagePullPolicy: Always`,
		},
		{
			`
configmap:
  baremetal:
    storageClass:
    - fast-disks:
        blockCleanerCommand:
        - /scripts/shred.sh
        - "2"
        hostDir: /mnt/fast-disks
daemonset:
  imagePullPolicy: IfNotPresent`,
			"baremetal",
			`
classes:
- name: fast-disks
  blockCleanerCommand:
  - /scripts/shred.sh
  - "2"
  hostDir: /mnt/fast-disks
daemonset:
  imagePullPolicy: IfNotPresent`,
		},
	}

	for _, v := range testcases {
		val, err := chartutil.ReadValues([]byte(v.in))
		if err != nil {
			t.Fatal(err)
		}
		expectedOut, err := chartutil.ReadValues([]byte(v.expected))
		if err != nil {
			t.Fatal(err)
		}
		out, err := upgrade(val, v.engine)
		if err != nil {
			t.Fatal(err)
		}
		outStr, err := out.YAML()
		if err != nil {
			t.Fatal(err)
		}
		expectedOutStr, err := expectedOut.YAML()
		if err != nil {
			t.Fatal(err)
		}
		if outStr != expectedOutStr {
			t.Errorf("expected %v, got: %v", expectedOutStr, outStr)
		}
	}
}
