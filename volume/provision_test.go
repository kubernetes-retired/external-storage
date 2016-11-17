/*
Copyright 2016 The Kubernetes Authors.

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
	"errors"
	"io/ioutil"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/kubernetes-incubator/nfs-provisioner/controller"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/api/resource"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/runtime"
	utiltesting "k8s.io/client-go/pkg/util/testing"
)

func TestCreateVolume(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name             string
		options          controller.VolumeOptions
		envKey           string
		expectedServer   string
		expectedPath     string
		expectedGroup    uint64
		expectedBlock    string
		expectedExportId uint16
		expectError      bool
	}{
		{
			name: "succeed creating volume",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-1",
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "1.1.1.1",
			expectedPath:     tmpDir + "/pvc-1",
			expectedGroup:    0,
			expectedBlock:    "\nExport_Id = 1;\n",
			expectedExportId: 1,
			expectError:      false,
		},
		{
			name: "succeed creating volume again",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-2",
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "1.1.1.1",
			expectedPath:     tmpDir + "/pvc-2",
			expectedGroup:    0,
			expectedBlock:    "\nExport_Id = 2;\n",
			expectedExportId: 2,
			expectError:      false,
		},
		{
			name: "bad parameter",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-3",
				Parameters: map[string]string{"foo": "bar"},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportId: 0,
			expectError:      true,
		},
		{
			name: "bad server",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-4",
				Parameters: map[string]string{},
			},
			envKey:           serviceEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportId: 0,
			expectError:      true,
		},
		{
			name: "dir already exists",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-1",
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportId: 0,
			expectError:      true,
		},
		{
			name: "error exporting",
			options: controller.VolumeOptions{
				Capacity:                      resource.MustParse("1Ki"),
				AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany},
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "FAIL_TO_EXPORT_ME",
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportId: 0,
			expectError:      true,
		},
	}

	client := fake.NewSimpleClientset()
	conf := tmpDir + "/test"
	_, err := os.Create(conf)
	if err != nil {
		t.Errorf("Error creating file %s: %v", conf, err)
	}
	p := newNFSProvisionerInternal(tmpDir+"/", client, &testExporter{config: conf})

	for _, test := range tests {
		os.Setenv(test.envKey, "1.1.1.1")

		server, path, supGroup, block, exportId, err := p.createVolume(test.options)

		evaluate(t, test.name, test.expectError, err, test.expectedServer, server, "server")
		evaluate(t, test.name, test.expectError, err, test.expectedPath, path, "path")
		evaluate(t, test.name, test.expectError, err, test.expectedGroup, supGroup, "group")
		evaluate(t, test.name, test.expectError, err, test.expectedBlock, block, "block")
		evaluate(t, test.name, test.expectError, err, test.expectedExportId, exportId, "export id")

		os.Unsetenv(test.envKey)
	}
}

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

	client := fake.NewSimpleClientset()
	p := newNFSProvisionerInternal(tmpDir+"/", client, &testExporter{})

	for _, test := range tests {
		gid, err := p.validateOptions(test.options)

		evaluate(t, test.name, test.expectError, err, test.expectedGid, gid, "gid")
	}
}

func TestCreateDirectory(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	fi, _ := os.Stat(tmpDir)
	defaultGid := fi.Sys().(*syscall.Stat_t).Gid

	tests := []struct {
		name         string
		directory    string
		gid          string
		expectedGid  uint32
		expectedPerm os.FileMode
		expectError  bool
	}{
		{
			name:         "gid none",
			directory:    "foo",
			gid:          "none",
			expectedGid:  defaultGid,
			expectedPerm: os.FileMode(0777),
			expectError:  false,
		},
		// {
		// 	name:         "gid 1001",
		// 	directory:    "bar",
		// 	gid:          "1001",
		// 	expectedGid:  1001,
		// 	expectedPerm: os.FileMode(0071),
		// 	expectError:  false,
		// },
		{
			name:         "path already exists",
			directory:    "foo",
			gid:          "none",
			expectedGid:  0,
			expectedPerm: 0,
			expectError:  true,
		},
		{
			name:         "bad gid",
			directory:    "baz",
			gid:          "foo",
			expectedGid:  0,
			expectedPerm: 0,
			expectError:  true,
		},
	}

	client := fake.NewSimpleClientset()
	p := newNFSProvisionerInternal(tmpDir+"/", client, &testExporter{})

	for _, test := range tests {
		path := p.exportDir + test.directory
		defer os.RemoveAll(path)

		err := p.createDirectory(test.directory, test.gid)

		var gid uint32
		var perm os.FileMode
		if !test.expectError {
			fi, err := os.Stat(path)
			if err != nil {
				t.Logf("test case: %s", test.name)
				t.Errorf("stat %s failed with error: %v", path, err)
			} else {
				gid = fi.Sys().(*syscall.Stat_t).Gid
				perm = fi.Mode().Perm()
			}
		}

		evaluate(t, test.name, test.expectError, err, test.expectedGid, gid, "gid owner")
		evaluate(t, test.name, test.expectError, err, test.expectedPerm, perm, "permission bits")
	}
}

func TestAddToRemoveFromFile(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	client := fake.NewSimpleClientset()
	conf := tmpDir + "/test"
	_, err := os.Create(conf)
	if err != nil {
		t.Errorf("Error creating file %s: %v", conf, err)
	}
	p := newNFSProvisionerInternal(tmpDir+"/", client, &testExporter{})

	toAdd := "abc\nxyz\n"
	p.addToFile(conf, toAdd)

	read, _ := ioutil.ReadFile(conf)
	if toAdd != string(read) {
		t.Errorf("Expected %s but got %s", toAdd, string(read))
	}

	toRemove := toAdd

	p.removeFromFile(conf, toRemove)
	read, _ = ioutil.ReadFile(conf)
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
		conf := tmpDir + "/test" + "-" + strconv.Itoa(i)
		err := ioutil.WriteFile(conf, []byte(test.configContents), 0755)
		if err != nil {
			t.Errorf("Error writing file %s: %v", conf, err)
		}

		exportIds, err := getConfigExportIds(conf, test.re)

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
		p := newNFSProvisionerInternal(tmpDir+"/", client, &testExporter{})

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
			{
				Addresses:         epAddresses,
				NotReadyAddresses: []v1.EndpointAddress{},
				Ports:             epPorts,
			},
		},
	}
}

type testExporter struct {
	config string
}

var _ exporter = &testExporter{}

func (e *testExporter) GetConfig() string {
	return e.config
}

func (e *testExporter) GetConfigExportIds() (map[uint16]bool, error) {
	return map[uint16]bool{}, nil
}

func (e *testExporter) CreateBlock(exportId, path string) string {
	return "\nExport_Id = " + exportId + ";\n"
}

func (e *testExporter) Export(path string) error {
	if strings.Contains(path, "FAIL_TO_EXPORT_ME") {
		return errors.New("fake error")
	}
	return nil
}

func (e *testExporter) Unexport(volume *v1.PersistentVolume) error {
	return nil
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
