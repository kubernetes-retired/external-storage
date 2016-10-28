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
	"github.com/wongma7/nfs-provisioner/controller"
	"k8s.io/client-go/1.4/kubernetes/fake"
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/unversioned"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/runtime"
	utiltesting "k8s.io/client-go/1.4/pkg/util/testing"

	"io/ioutil"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"testing"
)

func TestValidateOptions(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name        string
		options     controller.VolumeOptions
		expectedGid string
		expectError bool
	}{
		{
			name:        "empty parameters",
			options:     controller.VolumeOptions{Parameters: map[string]string{}, Capacity: resource.MustParse("1Ki")},
			expectedGid: "none",
			expectError: false,
		},
		{
			name:        "gid parameter value 'none'",
			options:     controller.VolumeOptions{Parameters: map[string]string{"gid": "none"}, Capacity: resource.MustParse("1Ki")},
			expectedGid: "none",
			expectError: false,
		},
		{
			name:        "gid parameter value id",
			options:     controller.VolumeOptions{Parameters: map[string]string{"gid": "1"}, Capacity: resource.MustParse("1Ki")},
			expectedGid: "1",
			expectError: false,
		},
		{
			name:        "bad parameter name",
			options:     controller.VolumeOptions{Parameters: map[string]string{"foo": "bar"}},
			expectedGid: "",
			expectError: true,
		},
		{
			name:        "bad gid parameter value string",
			options:     controller.VolumeOptions{Parameters: map[string]string{"gid": "foo"}},
			expectedGid: "",
			expectError: true,
		},
		{
			name:        "bad gid parameter value zero",
			options:     controller.VolumeOptions{Parameters: map[string]string{"gid": "0"}},
			expectedGid: "",
			expectError: true,
		},
		{
			name:        "bad gid parameter value negative",
			options:     controller.VolumeOptions{Parameters: map[string]string{"gid": "-1"}},
			expectedGid: "",
			expectError: true,
		},
		// TODO implement options.ProvisionerSelector parsing
		{
			name:        "non-nil selector",
			options:     controller.VolumeOptions{Selector: &unversioned.LabelSelector{MatchLabels: nil}},
			expectedGid: "",
			expectError: true,
		},
		{
			name:        "bad capacity",
			options:     controller.VolumeOptions{Capacity: resource.MustParse("1Ei")},
			expectedGid: "",
			expectError: true,
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset()
		path := tmpDir + "/test"
		_, err := os.Create(path)
		if err != nil {
			t.Errorf("Error creating file %s: %v", path, err)
		}
		p := newNFSProvisionerInternal(tmpDir, client, true, path)
		os.RemoveAll(path)

		gid, err := p.validateOptions(test.options)
		evaluate(t, test.name, test.expectError, err, test.expectedGid, gid, "gid")
	}
}

func TestAddToRemoveFromFile(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	client := fake.NewSimpleClientset()
	path := tmpDir + "/test"
	_, err := os.Create(path)
	if err != nil {
		t.Errorf("Error creating file %s: %v", path, err)
	}
	p := newNFSProvisionerInternal(tmpDir, client, true, path)

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

func TestGetConfigExportIds(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name              string
		useGanesha        bool
		configContents    string
		re                *regexp.Regexp
		expectedExportIds map[uint16]bool
		expectError       bool
	}{
		{
			name: "ganesha exports 1, 3",
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
		{
			name: "kernel exports 1, 3",
			configContents: "\n foo *(rw,insecure,root_squash,fsid=1)\n" +
				"\n bar *(rw,insecure,root_squash,fsid=3)\n",
			re:                regexp.MustCompile("fsid=([0-9]+)"),
			expectedExportIds: map[uint16]bool{1: true, 3: true},
			expectError:       false,
		},
		{
			name: "bad regex",
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
		exportIds, err := getConfigExportIds(path, test.re)
		evaluate(t, test.name, test.expectError, err, test.expectedExportIds, exportIds, "export ids")
	}
}

func TestGetServer(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	// service > node > podIP
	tests := []struct {
		name           string
		objs           []runtime.Object
		podIP          string
		service        string
		namespace      string
		node           string
		expectedServer string
		expectError    bool
	}{
		{
			name: "valid service",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "",
			expectedServer: "1.1.1.1",
			expectError:    false,
		},
		{
			name: "invalid service, ports don't match exactly",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {999999, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "",
			expectedServer: "",
			expectError:    true,
		},
		{
			name: "invalid service, points to different pod IP",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"3.3.3.3"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "",
			expectedServer: "",
			expectError:    true,
		},
		{
			name: "invalid service, should error even though valid node",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"3.3.3.3"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "127.0.0.1",
			expectedServer: "",
			expectError:    true,
		},
		{
			name: "valid service but no namespace",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "",
			node:           "",
			expectedServer: "",
			expectError:    true,
		},
		{
			name: "valid service, valid node, should use service",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "127.0.0.1",
			expectedServer: "1.1.1.1",
			expectError:    false,
		},
		{
			name:           "no service, valid node, should use node",
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			node:           "127.0.0.1",
			expectedServer: "127.0.0.1",
			expectError:    false,
		},
		{
			name:           "no service, no node, should use podIP",
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			node:           "",
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
		if test.node != "" {
			os.Setenv(nodeEnv, test.node)
		}

		client := fake.NewSimpleClientset(test.objs...)
		path := tmpDir + "/test"
		_, err := os.Create(path)
		if err != nil {
			t.Errorf("Error creating file %s: %v", path, err)
		}
		p := newNFSProvisionerInternal(tmpDir, client, true, path)
		os.RemoveAll(path)

		server, err := p.getServer()
		evaluate(t, test.name, test.expectError, err, test.expectedServer, server, "server")

		os.Unsetenv(podIPEnv)
		os.Unsetenv(serviceEnv)
		os.Unsetenv(namespaceEnv)
		os.Unsetenv(nodeEnv)
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
