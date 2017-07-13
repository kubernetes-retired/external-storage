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
	"path"
	"reflect"
	"testing"

	"k8s.io/api/core/v1"
)

const (
	dnsName    = "fs-47a2c22e.efs.us-west-2.amazonaws.com"
	mountpoint = "/mountpoint/data"
	source     = "/source/data"
)

func TestGetLocalPathToDelete(t *testing.T) {
	tests := []struct {
		name         string
		server       string
		path         string
		expectedPath string
		expectError  bool
	}{
		{
			name:         "pv path has corresponding local path",
			server:       dnsName,
			path:         path.Join(source, "pv/foo/bar"),
			expectedPath: path.Join(mountpoint, "pv/foo/bar"),
		},
		{
			name:        "server is different from provisioner's stored DNS name",
			server:      "foo",
			path:        path.Join(source, "pv"),
			expectError: true,
		},
		{
			name:        "pv path does not have corresponding local path",
			server:      dnsName,
			path:        path.Join("/foo", "pv"),
			expectError: true,
		},
	}
	efsProvisioner := newTestEFSProvisioner()
	for _, test := range tests {
		source := &v1.NFSVolumeSource{
			Server: test.server,
			Path:   test.path,
		}
		path, err := efsProvisioner.getLocalPathToDelete(source)
		evaluate(t, test.name, test.expectError, err, test.expectedPath, path, "local path to delete")
	}
}

func newTestEFSProvisioner() *efsProvisioner {
	return &efsProvisioner{
		dnsName:    dnsName,
		mountpoint: mountpoint,
		source:     source,
	}
}

func evaluate(t *testing.T, name string, expectError bool, err error, expected interface{}, got interface{}, output string) {
	if !expectError && err != nil {
		t.Logf("test case: %s", name)
		t.Errorf("unexpected error getting %s: %v", output, err)
	} else if expectError && err == nil {
		t.Logf("test case: %s", name)
		t.Errorf("expected error but got %s: %v", output, got)
	} else if !reflect.DeepEqual(expected, got) {
		t.Logf("test case: %s", name)
		t.Errorf("expected %s %v but got %s %v", output, expected, output, got)
	}
}
