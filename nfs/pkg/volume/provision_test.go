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
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	utiltesting "k8s.io/client-go/util/testing"
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
		expectedExportID uint16
		expectError      bool
		expectIgnored    bool
	}{
		{
			name: "succeed creating volume",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-1",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "1.1.1.1",
			expectedPath:     tmpDir + "/pvc-1",
			expectedGroup:    0,
			expectedBlock:    "\nExport_Id = 1;\n",
			expectedExportID: 1,
			expectError:      false,
		},
		{
			name: "succeed creating volume again",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-2",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "1.1.1.1",
			expectedPath:     tmpDir + "/pvc-2",
			expectedGroup:    0,
			expectedBlock:    "\nExport_Id = 2;\n",
			expectedExportID: 2,
			expectError:      false,
		},
		{
			name: "bad parameter",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-3",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{"foo": "bar"},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportID: 0,
			expectError:      true,
			expectIgnored:    false,
		},
		{
			name: "bad server",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-4",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           serviceEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportID: 0,
			expectError:      true,
			expectIgnored:    false,
		},
		{
			name: "dir already exists",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-1",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportID: 0,
			expectError:      true,
			expectIgnored:    false,
		},
		{
			name: "error exporting",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "FAIL_TO_EXPORT_ME",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportID: 0,
			expectError:      true,
		},
		{
			name: "succeed creating volume last slot",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-3",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "1.1.1.1",
			expectedPath:     tmpDir + "/pvc-3",
			expectedGroup:    0,
			expectedBlock:    "\nExport_Id = 3;\n",
			expectedExportID: 3,
			expectError:      false,
		},
		{
			name: "max export limit exceeded",
			options: controller.VolumeOptions{
				PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
				PVName:     "pvc-3",
				PVC:        newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil),
				Parameters: map[string]string{},
			},
			envKey:           podIPEnv,
			expectedServer:   "",
			expectedPath:     "",
			expectedGroup:    0,
			expectedBlock:    "",
			expectedExportID: 0,
			expectError:      true,
			expectIgnored:    true,
		},
	}

	client := fake.NewSimpleClientset()
	conf := tmpDir + "/test"
	_, err := os.Create(conf)
	if err != nil {
		t.Errorf("Error creating file %s: %v", conf, err)
	}
	exporter := &testExporter{
		exportMap: &exportMap{exportIDs: map[uint16]bool{}},
		config:    conf,
	}
	maxExports := 3
	p := newNFSProvisionerInternal(tmpDir+"/", client, false, exporter, newDummyQuotaer(), "", maxExports, "*")

	for _, test := range tests {
		os.Setenv(test.envKey, "1.1.1.1")

		volume, err := p.createVolume(test.options)
		if err == nil {
			p.exporter.(*testExporter).exportIDs[volume.exportID] = true
		}

		evaluate(t, test.name, test.expectError, err, test.expectedServer, volume.server, "server")
		evaluate(t, test.name, test.expectError, err, test.expectedPath, volume.path, "path")
		evaluate(t, test.name, test.expectError, err, test.expectedGroup, volume.supGroup, "group")
		evaluate(t, test.name, test.expectError, err, test.expectedBlock, volume.exportBlock, "block")
		evaluate(t, test.name, test.expectError, err, test.expectedExportID, volume.exportID, "export id")

		_, isIgnored := err.(*controller.IgnoredError)
		evaluate(t, test.name, test.expectError, err, test.expectIgnored, isIgnored, "ignored error")

		os.Unsetenv(test.envKey)
	}
}

func TestValidateOptions(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name               string
		options            controller.VolumeOptions
		expectedGid        string
		expectedRootSquash bool
		expectError        bool
	}{
		{
			name: "empty parameters",
			options: controller.VolumeOptions{
				Parameters: map[string]string{},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectedGid: "none",
			expectError: false,
		},
		{
			name: "gid parameter value 'none'",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"gid": "none"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectedGid: "none",
			expectError: false,
		},
		{
			name: "gid parameter value id",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"gid": "1"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
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
		{
			name: "root squash parameter value 'true'",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"rootSquash": "true"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectedGid:        "none",
			expectedRootSquash: true,
			expectError:        false,
		},
		{
			name: "root squash parameter value 'false'",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"rootSquash": "false"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectedGid:        "none",
			expectedRootSquash: false,
			expectError:        false,
		},
		{
			name: "bad root squash parameter value neither 'true' nor 'false'",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"rootSquash": "asdf"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectError: true,
		},

		// TODO implement options.ProvisionerSelector parsing
		{
			name: "mount options parameter key",
			options: controller.VolumeOptions{
				Parameters: map[string]string{"mountOptions": "asdf"},
				PVC:        newClaim(resource.MustParse("1Ki"), nil, nil),
			},
			expectedGid: "none",
			expectError: false,
		},
		// TODO implement options.ProvisionerSelector parsing
		{
			name: "non-nil selector",
			options: controller.VolumeOptions{
				PVC: newClaim(resource.MustParse("1Ki"), nil, &metav1.LabelSelector{MatchLabels: nil}),
			},
			expectedGid: "",
			expectError: true,
		},
		{
			name: "bad capacity",
			options: controller.VolumeOptions{
				PVC: newClaim(resource.MustParse("1Ei"), nil, nil),
			},
			expectedGid: "",
			expectError: true,
		},
	}

	client := fake.NewSimpleClientset()
	p := newNFSProvisionerInternal(tmpDir+"/", client, false, &testExporter{}, newDummyQuotaer(), "", -1, "*")

	for _, test := range tests {
		gid, rootSquash, _, err := p.validateOptions(test.options)

		evaluate(t, test.name, test.expectError, err, test.expectedGid, gid, "gid")
		evaluate(t, test.name, test.expectError, err, test.expectedRootSquash, rootSquash, "root squash")
	}
}

func TestShouldProvision(t *testing.T) {
	claim := newClaim(resource.MustParse("1Ki"), []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce, v1.ReadOnlyMany}, nil)
	evaluateExportTests(t, "ShouldProvision", func(p *nfsProvisioner) bool {
		return p.ShouldProvision(claim)
	})
}

func TestCheckExportLimit(t *testing.T) {
	evaluateExportTests(t, "checkExportLimit", func(p *nfsProvisioner) bool {
		return p.checkExportLimit()
	})
}

func evaluateExportTests(t *testing.T, output string, checker func(*nfsProvisioner) bool) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name           string
		configContents string
		exportIDs      map[uint16]bool
		maxExports     int
		expectedResult bool
		expectError    bool
	}{
		{
			name:           "unlimited exports",
			exportIDs:      map[uint16]bool{1: true, 3: true},
			maxExports:     -1,
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "max export limit reached",
			exportIDs:      map[uint16]bool{1: true, 3: true},
			maxExports:     2,
			expectedResult: false,
			expectError:    false,
		},
		{
			name:           "max export limit not reached",
			exportIDs:      map[uint16]bool{1: true},
			maxExports:     2,
			expectedResult: true,
			expectError:    false,
		},
	}
	for _, test := range tests {
		client := fake.NewSimpleClientset()
		p := newNFSProvisionerInternal(tmpDir+"/", client, false, &testExporter{exportMap: &exportMap{exportIDs: test.exportIDs}}, newDummyQuotaer(), "", test.maxExports, "*")
		ok := checker(p)
		evaluate(t, test.name, test.expectError, nil, test.expectedResult, ok, output)
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
	p := newNFSProvisionerInternal(tmpDir+"/", client, false, &testExporter{}, newDummyQuotaer(), "", -1, "*")

	for _, test := range tests {
		path := p.exportDir + test.directory
		defer os.RemoveAll(path)

		err := p.createDirectory(test.directory, test.gid)

		var gid uint32
		var perm os.FileMode
		var fi os.FileInfo
		if !test.expectError {
			fi, err = os.Stat(path)
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

	conf := tmpDir + "/test"
	_, err := os.Create(conf)
	if err != nil {
		t.Errorf("Error creating file %s: %v", conf, err)
	}

	toAdd := "abc\nxyz\n"
	addToFile(&sync.Mutex{}, conf, toAdd)

	read, _ := ioutil.ReadFile(conf)
	if toAdd != string(read) {
		t.Errorf("Expected %s but got %s", toAdd, string(read))
	}

	toRemove := toAdd

	removeFromFile(&sync.Mutex{}, conf, toRemove)
	read, _ = ioutil.ReadFile(conf)
	if "" != string(read) {
		t.Errorf("Expected %s but got %s", "", string(read))
	}
}

func TestGetExistingIDs(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name              string
		useGanesha        bool
		configContents    string
		re                *regexp.Regexp
		expectedExportIDs map[uint16]bool
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
			expectedExportIDs: map[uint16]bool{1: true, 3: true},
			expectError:       false,
		},
		{
			name: "kernel exports 1, 3",
			configContents: "\n foo *(rw,insecure,root_squash,fsid=1)\n" +
				"\n bar *(rw,insecure,root_squash,fsid=3)\n",
			re:                regexp.MustCompile("fsid=([0-9]+)"),
			expectedExportIDs: map[uint16]bool{1: true, 3: true},
			expectError:       false,
		},
		{
			name: "bad regex",
			configContents: "\nEXPORT\n{\n" +
				"\tExport_Id = 1;\n" +
				"\tFilesystem_id = 1.1;\n" +
				"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n",
			re:                regexp.MustCompile("Export_Id = [0-9]+;"),
			expectedExportIDs: map[uint16]bool{},
			expectError:       true,
		},
	}
	for i, test := range tests {
		conf := tmpDir + "/test" + "-" + strconv.Itoa(i)
		err := ioutil.WriteFile(conf, []byte(test.configContents), 0755)
		if err != nil {
			t.Errorf("Error writing file %s: %v", conf, err)
		}

		exportIDs, err := getExistingIDs(conf, test.re)

		evaluate(t, test.name, test.expectError, err, test.expectedExportIDs, exportIDs, "export ids")
	}
}

func TestGetServer(t *testing.T) {
	tmpDir := utiltesting.MkTmpdirOrDie("nfsProvisionTest")
	defer os.RemoveAll(tmpDir)

	// It should be node or service...but in case both exist, instead of failing
	// just test for node > service > podIP (doesn't really matter after all)
	tests := []struct {
		name           string
		objs           []runtime.Object
		podIP          string
		service        string
		namespace      string
		node           string
		serverHostname string
		outOfCluster   bool
		expectedServer string
		expectError    bool
	}{

		{
			name:           "valid node only",
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			node:           "127.0.0.1",
			expectedServer: "127.0.0.1",
			expectError:    false,
		},
		{
			name: "valid node, valid service, should use node",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "127.0.0.1",
			expectedServer: "127.0.0.1",
			expectError:    false,
		},
		{
			name: "invalid service, valid node, should use node",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"3.3.3.3"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "2.2.2.2",
			service:        "foo",
			namespace:      "default",
			node:           "127.0.0.1",
			expectedServer: "127.0.0.1",
			expectError:    false,
		},
		{
			name: "valid service only",
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
			name: "service but no pod IP to check if valid",
			objs: []runtime.Object{
				newService("foo", "1.1.1.1"),
				newEndpoints("foo", []string{"2.2.2.2"}, []endpointPort{{2049, v1.ProtocolTCP}, {20048, v1.ProtocolTCP}, {111, v1.ProtocolUDP}, {111, v1.ProtocolTCP}}),
			},
			podIP:          "",
			service:        "foo",
			namespace:      "",
			node:           "",
			expectedServer: "",
			expectError:    true,
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
		{
			name:           "server-hostname is ignored, valid node",
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			node:           "127.0.0.1",
			serverHostname: "foo",
			expectedServer: "127.0.0.1",
			expectError:    false,
		},
		{
			name:           "server-hostname takes precedence when out-of-cluster",
			objs:           []runtime.Object{},
			podIP:          "2.2.2.2",
			service:        "",
			namespace:      "",
			node:           "127.0.0.1",
			serverHostname: "foo",
			outOfCluster:   true,
			expectedServer: "foo",
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
		p := newNFSProvisionerInternal(tmpDir+"/", client, test.outOfCluster, &testExporter{}, newDummyQuotaer(), test.serverHostname, -1, "*")

		server, err := p.getServer()

		evaluate(t, test.name, test.expectError, err, test.expectedServer, server, "server")

		os.Unsetenv(podIPEnv)
		os.Unsetenv(serviceEnv)
		os.Unsetenv(namespaceEnv)
		os.Unsetenv(nodeEnv)
	}
}

func newClaim(capacity resource.Quantity, accessmodes []v1.PersistentVolumeAccessMode, selector *metav1.LabelSelector) *v1.PersistentVolumeClaim {
	claim := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: accessmodes,
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): capacity,
				},
			},
			Selector: selector,
		},
		Status: v1.PersistentVolumeClaimStatus{},
	}
	return claim
}

func newService(name, clusterIP string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
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
		ObjectMeta: metav1.ObjectMeta{
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
	*exportMap

	config string
}

var _ exporter = &testExporter{}

func (e *testExporter) CanExport(limit int) bool {
	if e.exportMap != nil {
		return e.exportMap.CanExport(limit)
	}
	return true
}

func (e *testExporter) AddExportBlock(path string, _ bool, _ string) (string, uint16, error) {
	id := uint16(1)
	for ; id <= math.MaxUint16; id++ {
		if _, ok := e.exportIDs[id]; !ok {
			break
		}
	}
	return fmt.Sprintf("\nExport_Id = %d;\n", id), id, nil
}

func (e *testExporter) RemoveExportBlock(block string, exportID uint16) error {
	return nil
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
