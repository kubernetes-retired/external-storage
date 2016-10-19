/*
Copyright 2016 Red Hat, Inc.

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

package volume

import (
	"k8s.io/client-go/1.4/kubernetes/fake"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/runtime"
	utiltesting "k8s.io/client-go/1.4/pkg/util/testing"

	// "flag"
	"io/ioutil"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"testing"
)

func TestAddToRemoveFromFile(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	client := fake.NewSimpleClientset()
	path := tmpDir + "/test"
	_, err := os.Create(path)
	if err != nil {
		t.Errorf("Error creating file %s: %v", path, err)
	}
	p := newProvisionerInternal(tmpDir, client, nil, true, path)

	toAdd := "abc\nxyz\n"
	p.addToFile(path, toAdd)

	read, _ := ioutil.ReadFile(path)
	if toAdd != string(read) {
		t.Errorf("Expected %s but got %s", toAdd, string(read))
	}

	toRemove := toAdd

	p.removeFromFile(path, toRemove)
	read, _ = ioutil.ReadFile(path)
	if "" != string(read) {
		t.Errorf("Expected %s but got %s", "", string(read))
	}
}

func TestGetExportIds(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		useGanesha        bool
		configContents    string
		re                *regexp.Regexp
		expectedExportIds map[uint16]bool
		expectError       bool
	}{
		// ganesha exports 1, 3
		{
			configContents: "\nEXPORT\n{\n" +
				"\tExport_Id = 1;\n" +
				"\tFilesystem_id = 1.1;\n" +
				"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n" +
				"\nEXPORT\n{\n" +
				"\tExport_Id = 3;\n" +
				"\tFilesystem_id = 1.1;\n" +
				"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n",
			re:                regexp.MustCompile("Export_Id = ([0-9]+);"),
			expectedExportIds: map[uint16]bool{1: true, 3: true},
			expectError:       false,
		},
		// kernel exports 1, 3
		{
			configContents: "\n foo *(rw,insecure,root_squash,fsid=1)\n" +
				"\n bar *(rw,insecure,root_squash,fsid=3)\n",
			re:                regexp.MustCompile("fsid=([0-9]+)"),
			expectedExportIds: map[uint16]bool{1: true, 3: true},
			expectError:       false,
		},
		// bad regex
		{
			configContents: "\nEXPORT\n{\n" +
				"\tExport_Id = 1;\n" +
				"\tFilesystem_id = 1.1;\n" +
				"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n",
			re:                regexp.MustCompile("Export_Id = [0-9]+;"),
			expectedExportIds: map[uint16]bool{},
			expectError:       true,
		},
	}
	for i, test := range tests {
		path := tmpDir + "/test" + "-" + strconv.Itoa(i)
		err := ioutil.WriteFile(path, []byte(test.configContents), 0755)
		if err != nil {
			t.Errorf("Error writing file %s: %v", path, err)
		}

		exportIds, err := getExportIds(path, test.re)
		if !test.expectError && err != nil {
			t.Errorf("Error getting export ids: %v", err)
		}
		if !reflect.DeepEqual(test.expectedExportIds, exportIds) {
			t.Errorf("Expected %v but got %v", test.expectedExportIds, exportIds)
		}
	}
}

func TestGetServer(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		objs           []runtime.Object
		podIP          string
		service        string
		namespace      string
		expectedServer string
		expectError    bool
	}{
		// valid service
		{
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			expectedServer: "1.1.1.1",
			expectError:    false,
		},
		// invalid service, ports don't match exactly
		{
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {999999, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			expectedServer: "",
			expectError:    true,
		},
		// invalid service, points to different pod IP
		{
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"3.3.3.3"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			expectedServer: "",
			expectError:    true,
		},
		// service but no namespace
		{
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "",
			expectedServer: "",
			expectError:    true,
		},
		// no service, should fallback to podIP
		{
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			expectedServer: "2.2.2.2",
			expectError:    false,
		},
	}
	for _, test := range tests {
		if test.podIP != "" {
			os.Setenv(podIPEnv, test.podIP)
		}
		if test.service != "" {
			os.Setenv(serviceEnv, test.service)
		}
		if test.namespace != "" {
			os.Setenv(namespaceEnv, test.namespace)
		}

		client := fake.NewSimpleClientset(test.objs...)
		path := tmpDir + "/test"
		_, err := os.Create(path)
		if err != nil {
			t.Errorf("Error creating file %s: %v", path, err)
		}
		p := newProvisionerInternal(tmpDir, client, nil, true, path)

		server, err := p.getServer()
		if !test.expectError && err != nil {
			t.Errorf("Error getting server: %v", err)
		}
		if test.expectedServer != server {
			t.Errorf("Expected %s but got %s", test.expectedServer, server)
		}

		os.Unsetenv(podIPEnv)
		os.Unsetenv(serviceEnv)
		os.Unsetenv(namespaceEnv)
	}
}

func newService(name, clusterIP string) *v1.Service {
	return &v1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			ClusterIP: clusterIP,
		},
	}
}

type endpointPort struct {
	port     int32
	protocol v1.Protocol
}

func newEndpoints(name string, ips []string, ports []endpointPort) *v1.Endpoints {
	epAddresses := []v1.EndpointAddress{}
	for _, ip := range ips {
		epAddresses = append(epAddresses, v1.EndpointAddress{IP: ip, Hostname: "", NodeName: nil, TargetRef: nil})
	}
	epPorts := []v1.EndpointPort{}
	for i, port := range ports {
		epPorts = append(epPorts, v1.EndpointPort{Name: strconv.Itoa(i), Port: port.port, Protocol: port.protocol})
	}
	return &v1.Endpoints{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Subsets: []v1.EndpointSubset{
			v1.EndpointSubset{
				Addresses:         epAddresses,
				NotReadyAddresses: []v1.EndpointAddress{},
				Ports:             epPorts,
			},
		},
	}
}
