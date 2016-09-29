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
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	"github.com/guelfey/go.dbus"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

const exportDir = "/export/"
const ganeshaConfig = "/export/_vfs.conf"

// are we allowed to set this? else make up our own
const annCreatedBy = "kubernetes.io/createdby"
const createdBy = "nfs-dynamic-provisioner"

// The entire EXPORT block, useful but not needed for deletion.
const annBlock = "EXPORT_block"

// The Export_Id of this PV's backing ganesha EXPORT, needed for deletion.
const annExportId = "Export_Id"

// Incremented for assigning each export a unique ID
var nextExportId = 0

// Lock for writing to the ganesha config file
var mutex = &sync.Mutex{}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume
// TODO upstream does plugin.NewProvisioner and can take advantage of the plugin framework e.g. awsElasticBlockStore has, and uses, manager (.CreateVolume) and plugin (...GetCloudProvider). Find a nicer way to pass the client through the Provisioner?
func Provision(options VolumeOptions, client kubernetes.Interface) (*v1.PersistentVolume, error) {
	// instead of createVolume could call out a script of some kind
	server, path, block, exportId, err := createVolume(options, client)
	if err != nil {
		return nil, err
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:   options.PVName,
			Labels: map[string]string{},
			Annotations: map[string]string{
				annCreatedBy: createdBy,
				annBlock:     block,
				annExportId:  strconv.Itoa(exportId),
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.Capacity,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

// createVolume creates a volume i.e. the storage asset. It creates a unique
// directory under /export (which could be the mountpoint of some persistent
// storage or just the ephemeral container directory) and exports it.
func createVolume(options VolumeOptions, client kubernetes.Interface) (string, string, string, int, error) {
	// TODO take and validate Parameters
	if options.Parameters != nil {
		return "", "", "", 0, fmt.Errorf("invalid parameter: no StorageClass parameters are supported")
	}

	// TODO implement options.ProvisionerSelector parsing
	// TODO pv.Labels MUST be set to match claim.spec.selector
	if options.Selector != nil {
		return "", "", "", 0, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	server, err := getServer(client)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("error getting NFS server IP for created volume: %v", err)
	}

	// TODO quota, something better than just directories
	// TODO figure out permissions: gid, chgrp, root_squash
	// Create the path for the volume unless it already exists. It has to exist
	// when AddExport is called.
	path := fmt.Sprintf(exportDir+"%s", options.PVName)
	if _, err := os.Stat(path); err == nil {
		return "", "", "", 0, fmt.Errorf("error creating volume, the path already exists")
	}
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", "", "", 0, fmt.Errorf("error creating dir for volume: %v", err)
	}

	// Add the export block to the ganesha config file
	block, exportId, err := addExportBlock(path)
	if err != nil {
		os.RemoveAll(path)
		return "", "", "", 0, fmt.Errorf("error adding export block to the ganesha config file: %v", err)
	}

	// Call AddExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		os.RemoveAll(path)
		removeExportBlock(block)
		return "", "", "", 0, fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.AddExport", 0, ganeshaConfig, fmt.Sprintf("export(path = %s)", path))
	if call.Err != nil {
		os.RemoveAll(path)
		removeExportBlock(block)
		return "", "", "", 0, fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.AddExport: %v", call.Err)
	}

	return server, path, block, exportId, nil
}

// addExportBlock adds an EXPORT block to the ganesha config file. It returns
// the added block, the Export_Id of the block, and an error if any.
func addExportBlock(path string) (string, int, error) {
	mutex.Lock()
	read, err := ioutil.ReadFile(ganeshaConfig)
	if err != nil {
		mutex.Unlock()
		return "", 0, err
	}

	// TODO there's probably a better way to do this. HAVE to assign unique IDs
	// across restarts, etc.
	// If zero, this is the first add: find the maximum existing ID and the next
	// ID to assign will be that maximum plus 1. Otherwise just keep incrementing.
	if nextExportId == 0 {
		re := regexp.MustCompile("Export_Id = [0-9]+;")
		lines := re.FindAll(read, -1)
		for _, line := range lines {
			digits := regexp.MustCompile("[0-9]+").Find(line)
			if id, _ := strconv.Atoi(string(digits)); id > nextExportId {
				nextExportId = id
			}
		}
	}
	nextExportId++

	exportId := nextExportId
	block := "\nEXPORT\n{\n"
	block = block + "\tExport_Id = " + strconv.Itoa(exportId) + ";\n"
	block = block + "\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"

	file, err := os.OpenFile(ganeshaConfig, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		mutex.Unlock()
		return "", 0, err
	}
	defer file.Close()

	if _, err = file.WriteString(block); err != nil {
		mutex.Unlock()
		return "", 0, err
	}
	file.Sync()

	mutex.Unlock()

	return block, exportId, nil
}

// removeExportBlock removes the given EXPORT block from the ganesha config file
func removeExportBlock(block string) error {
	mutex.Lock()
	read, err := ioutil.ReadFile(ganeshaConfig)
	if err != nil {
		mutex.Unlock()
		return err
	}

	removed := strings.Replace(string(read), block, "", -1)

	err = ioutil.WriteFile(ganeshaConfig, []byte(removed), 0)
	if err != nil {
		mutex.Unlock()
		return err
	}

	mutex.Unlock()
	return nil
}

func getServer(client kubernetes.Interface) (string, error) {
	// Use either `hostname -i` or MY_POD_IP as the fallback server
	var fallbackServer string
	podIP := os.Getenv("MY_POD_IP")
	if podIP == "" {
		glog.Info("env MY_POD_IP isn't set or provisioner isn't running as a pod")
		out, err := exec.Command("hostname", "-i").Output()
		if err != nil {
			return "", fmt.Errorf("hostname -i failed with error: %v, output: %s", err, out)
		}
		fallbackServer = string(out)
	} else {
		fallbackServer = podIP
	}

	// Try to use the service's cluster IP as the server if MY_SERVICE_NAME is
	// specified. Otherwise, use fallback here.
	serviceName := os.Getenv("MY_SERVICE_NAME")
	if serviceName == "" {
		glog.Info("env MY_SERVICE_NAME isn't set, falling back to using `hostname -i` or pod IP as server IP")
		return fallbackServer, nil
	}

	// From this point forward, rather than fallback & provision non-persistent
	// where persistent is expected, just return an error.
	namespace := os.Getenv("MY_POD_NAMESPACE")
	if namespace == "" {
		return "", fmt.Errorf("env MY_SERVICE_NAME is set but MY_POD_NAMESPACE isn't; no way to get the service cluster IP")
	}
	service, err := client.Core().Services(namespace).Get(serviceName)
	if err != nil {
		return "", fmt.Errorf("error getting service MY_SERVICE_NAME=%s in MY_POD_NAMESPACE=%s", serviceName, namespace)
	}

	// Do some validation of the service before provisioning useless volumes
	valid := false
	type endpointPort struct {
		port     int32
		protocol v1.Protocol
	}
	expectedPorts := map[endpointPort]bool{
		endpointPort{2049, v1.ProtocolTCP}:  true,
		endpointPort{20048, v1.ProtocolTCP}: true,
		endpointPort{111, v1.ProtocolUDP}:   true,
		endpointPort{111, v1.ProtocolTCP}:   true,
	}
	endpoints, err := client.Core().Endpoints(namespace).Get(serviceName)
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) != 1 {
			continue
		}
		if subset.Addresses[0].IP != fallbackServer {
			continue
		}
		actualPorts := make(map[endpointPort]bool)
		for _, port := range subset.Ports {
			actualPorts[endpointPort{port.Port, port.Protocol}] = true
		}
		if !reflect.DeepEqual(expectedPorts, actualPorts) {
			continue
		}
		valid = true
		break
	}
	if !valid {
		return "", fmt.Errorf("service MY_SERVICE_NAME=%s is not valid; check that it has for ports %v one endpoint, this pod's IP %v", serviceName, expectedPorts, fallbackServer)
	}
	if service.Spec.ClusterIP == v1.ClusterIPNone {
		return "", fmt.Errorf("service MY_SERVICE_NAME=%s is valid but it doesn't have a cluster IP", serviceName)
	}

	return service.Spec.ClusterIP, nil
}
