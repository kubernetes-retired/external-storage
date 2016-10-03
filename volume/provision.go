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

// are we allowed to set this? else make up our own
const annCreatedBy = "kubernetes.io/createdby"
const createdBy = "nfs-dynamic-provisioner"

// An annotation for the entire ganesha EXPORT block, useful but not needed for
// deletion.
const annBlock = "EXPORT_block"

// An annotation for the Export_Id of this PV's backing ganesha EXPORT, needed
// for deletion.
const annExportId = "Export_Id"

// An annotation for the line in /etc/exports, needed for deletion.
const annLine = "etcexports_line"

func NewNFSProvisioner(exportDir string, ganeshaConfig string, useGanesha bool, client kubernetes.Interface) Provisioner {
	return &nfsProvisioner{
		exportDir:     exportDir,
		ganeshaConfig: ganeshaConfig,
		useGanesha:    useGanesha,
		client:        client,
		nextExportId:  0,
		mutex:         &sync.Mutex{},
	}
}

type nfsProvisioner struct {
	// The directory to create PV-backing directories in
	exportDir string

	// The path of the NFS Ganesha configuration file
	ganeshaConfig string

	// Whether to use NFS Ganesha (D-Bus method calls) or kernel NFS server
	// (exportfs)
	useGanesha bool

	// Client, needed for getting a service cluster IP to put as the NFS server of
	// provisioned PVs
	client kubernetes.Interface

	// Incremented for assigning each export a unique ID, required by ganesha
	nextExportId int

	// Lock for writing to the ganesha config or /etc/exports file
	mutex *sync.Mutex
}

var _ Provisioner = &nfsProvisioner{}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume.
func (p *nfsProvisioner) Provision(options VolumeOptions) (*v1.PersistentVolume, error) {
	server, path, added, exportId, err := p.createVolume(options)
	if err != nil {
		return nil, err
	}

	annotations := make(map[string]string)
	annotations[annCreatedBy] = createdBy
	if p.useGanesha {
		annotations[annBlock] = added
		annotations[annExportId] = strconv.Itoa(exportId)
	} else {
		annotations[annLine] = added
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:        options.PVName,
			Labels:      map[string]string{},
			Annotations: annotations,
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
// directory under /export and exports it. Returns the server IP and the path of
// the directory. Also returns the block or line it added to either the ganesha
// config or /etc/exports, respectively. If using ganesha, returns a non-zero
// Export_Id.
func (p *nfsProvisioner) createVolume(options VolumeOptions) (string, string, string, int, error) {
	// TODO take and validate Parameters
	if options.Parameters != nil {
		return "", "", "", 0, fmt.Errorf("invalid parameter: no StorageClass parameters are supported")
	}

	// TODO implement options.ProvisionerSelector parsing
	// TODO pv.Labels MUST be set to match claim.spec.selector
	if options.Selector != nil {
		return "", "", "", 0, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	server, err := p.getServer()
	if err != nil {
		return "", "", "", 0, fmt.Errorf("error getting NFS server IP for created volume: %v", err)
	}

	// TODO quota, something better than just directories
	// TODO figure out permissions: gid, chgrp, root_squash
	// Create the path for the volume unless it already exists. It has to exist
	// when AddExport or exportfs is called.
	path := fmt.Sprintf(p.exportDir+"%s", options.PVName)
	if _, err := os.Stat(path); err == nil {
		return "", "", "", 0, fmt.Errorf("error creating volume, the path already exists")
	}
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", "", "", 0, fmt.Errorf("error creating dir for volume: %v", err)
	}

	if p.useGanesha {
		block, exportId, err := p.ganeshaExport(path)
		if err != nil {
			os.RemoveAll(path)
			return "", "", "", 0, err
		}
		return server, path, block, exportId, nil
	} else {
		line, err := p.kernelExport(path)
		if err != nil {
			os.RemoveAll(path)
			return "", "", "", 0, err
		}
		return server, path, line, 0, nil
	}
}

// getServer gets the server IP to put in a provisioned PV's spec.
func (p *nfsProvisioner) getServer() (string, error) {
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
	service, err := p.client.Core().Services(namespace).Get(serviceName)
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
	endpoints, err := p.client.Core().Endpoints(namespace).Get(serviceName)
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

// ganeshaExport exports the given directory using NFS Ganesha, assuming it is
// running and can be connected to using D-Bus. Returns the block it added to
// the ganesha config file and the block's Export_Id.
// https://github.com/nfs-ganesha/nfs-ganesha/wiki/Dbusinterface
func (p *nfsProvisioner) ganeshaExport(path string) (string, int, error) {
	// Create the export block to add to the ganesha config file
	p.mutex.Lock()
	read, err := ioutil.ReadFile(p.ganeshaConfig)
	if err != nil {
		p.mutex.Unlock()
		return "", 0, err
	}
	// TODO there's probably a better way to do this. HAVE to assign unique IDs
	// across restarts, etc.
	// If zero, this is the first add: find the maximum existing ID and the next
	// ID to assign will be that maximum plus 1. Otherwise just keep incrementing.
	if p.nextExportId == 0 {
		re := regexp.MustCompile("Export_Id = [0-9]+;")
		lines := re.FindAll(read, -1)
		for _, line := range lines {
			digits := regexp.MustCompile("[0-9]+").Find(line)
			if id, _ := strconv.Atoi(string(digits)); id > p.nextExportId {
				p.nextExportId = id
			}
		}
	}
	p.nextExportId++
	exportId := p.nextExportId
	p.mutex.Unlock()

	block := "\nEXPORT\n{\n"
	block = block + "\tExport_Id = " + strconv.Itoa(exportId) + ";\n"
	block = block + "\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"

	// Add the export block to the ganesha config file
	if err := p.addToFile(p.ganeshaConfig, block); err != nil {
		return "", 0, fmt.Errorf("error adding export block to the ganesha config file: %v", err)
	}

	// Call AddExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		p.removeFromFile(p.ganeshaConfig, block)
		return "", 0, fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.AddExport", 0, p.ganeshaConfig, fmt.Sprintf("export(path = %s)", path))
	if call.Err != nil {
		p.removeFromFile(p.ganeshaConfig, block)
		return "", 0, fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.AddExport: %v", call.Err)
	}

	return block, exportId, nil
}

// kernelExport exports the given directory using the NFS server, assuming it is
// running. Returns the line it added to /etc/exports.
func (p *nfsProvisioner) kernelExport(path string) (string, error) {
	line := "\n" + path + " *(rw,insecure,no_root_squash)\n"

	// Add the export directory line to /etc/exports
	if err := p.addToFile("/etc/exports", line); err != nil {
		return "", fmt.Errorf("error adding export directory to /etc/exports: %v", err)
	}

	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.removeFromFile("/etc/exports", line)
		return "", fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return line, nil
}

func (p *nfsProvisioner) addToFile(path string, toAdd string) error {
	p.mutex.Lock()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	defer file.Close()

	if _, err = file.WriteString(toAdd); err != nil {
		p.mutex.Unlock()
		return err
	}
	file.Sync()

	p.mutex.Unlock()
	return nil
}

func (p *nfsProvisioner) removeFromFile(path string, toRemove string) error {
	p.mutex.Lock()

	read, err := ioutil.ReadFile(path)
	if err != nil {
		p.mutex.Unlock()
		return err
	}

	removed := strings.Replace(string(read), toRemove, "", -1)

	err = ioutil.WriteFile(path, []byte(removed), 0)
	if err != nil {
		p.mutex.Unlock()
		return err
	}

	p.mutex.Unlock()
	return nil
}
