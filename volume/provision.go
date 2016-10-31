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
	"math"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/golang/glog"
	"github.com/guelfey/go.dbus"
	"github.com/wongma7/nfs-provisioner/controller"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

const (
	// VolumeGidAnnotationKey is the key of the annotation on the PersistentVolume
	// object that specifies a supplemental GID.
	VolumeGidAnnotationKey = "pv.beta.kubernetes.io/gid"

	// A PV annotation for the entire ganesha EXPORT block or /etc/exports
	// block, needed for deletion.
	annBlock = "EXPORT_block"

	// A PV annotation for the exportId of this PV's backing ganesha/kernel export
	// , needed for ganesha deletion and used for deleting the entry in exportIds
	// map so the id can be reassigned.
	annExportId = "Export_Id"

	// are we allowed to set this? else make up our own
	annCreatedBy = "kubernetes.io/createdby"
	createdBy    = "nfs-dynamic-provisioner"

	podIPEnv     = "POD_IP"
	serviceEnv   = "SERVICE_NAME"
	namespaceEnv = "POD_NAMESPACE"
	nodeEnv      = "NODE_NAME"
)

func NewNFSProvisioner(exportDir string, client kubernetes.Interface, useGanesha bool, ganeshaConfig string) controller.Provisioner {
	return newNFSProvisionerInternal(exportDir, client, useGanesha, ganeshaConfig)
}

func newNFSProvisionerInternal(exportDir string, client kubernetes.Interface, useGanesha bool, ganeshaConfig string) *nfsProvisioner {
	provisioner := &nfsProvisioner{
		// TODO exportDir must have trailing slash!
		exportDir:    exportDir,
		client:       client,
		mapMutex:     &sync.Mutex{},
		fileMutex:    &sync.Mutex{},
		podIPEnv:     podIPEnv,
		serviceEnv:   serviceEnv,
		namespaceEnv: namespaceEnv,
		nodeEnv:      nodeEnv,
	}

	if useGanesha {
		provisioner.exporter = &ganeshaExporter{ganeshaConfig: ganeshaConfig}
	} else {
		provisioner.exporter = &kernelExporter{}
	}
	var err error
	provisioner.exportIds, err = provisioner.exporter.GetConfigExportIds()
	if err != nil {
		glog.Errorf("error while populating exportIds map, there may be errors exporting later if exportIds are reused: %v", err)
	}

	return provisioner
}

type nfsProvisioner struct {
	// The directory to create PV-backing directories in
	exportDir string

	// Client, needed for getting a service cluster IP to put as the NFS server of
	// provisioned PVs
	client kubernetes.Interface

	// The exporter to use for exporting NFS shares
	exporter exporter

	// Map to track used exportIds. Each ganesha export needs a unique Export_Id,
	// and both ganesha and kernel exports need a unique fsid. So we simply assign
	// each export an exportId and use it as both Export_id and fsid.
	exportIds map[uint16]bool

	// Lock for accessing exportIds
	mapMutex *sync.Mutex

	// Lock for writing to the ganesha config or /etc/exports file
	fileMutex *sync.Mutex

	// Environment variables the provisioner pod needs valid values for in order to
	// put a service cluster IP as the server of provisioned NFS PVs, passed in
	// via downward API. If serviceEnv is set, namespaceEnv must be too.
	podIPEnv     string
	serviceEnv   string
	namespaceEnv string
	nodeEnv      string
}

var _ controller.Provisioner = &nfsProvisioner{}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume.
func (p *nfsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	server, path, supGroup, block, exportId, err := p.createVolume(options)
	if err != nil {
		return nil, err
	}

	annotations := make(map[string]string)
	annotations[annCreatedBy] = createdBy
	annotations[annExportId] = strconv.FormatUint(uint64(exportId), 10)
	annotations[annBlock] = block
	if supGroup != 0 {
		annotations[VolumeGidAnnotationKey] = strconv.FormatUint(supGroup, 10)
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
// directory under /export and exports it. Returns the server IP, the path, a
// zero/non-zero supplemental group, the block it added to either the ganesha
// config or /etc/exports, and the exportId
func (p *nfsProvisioner) createVolume(options controller.VolumeOptions) (string, string, uint64, string, uint16, error) {
	gid, err := p.validateOptions(options)
	if err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error validating options for volume: %v", err)
	}

	server, err := p.getServer()
	if err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error getting NFS server IP for volume: %v", err)
	}

	path := fmt.Sprintf(p.exportDir+"%s", options.PVName)

	err = p.createDirectory(options.PVName, gid)
	if err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error creating directory for volume: %v", err)
	}

	block, exportId, err := p.createExport(options.PVName)
	if err != nil {
		os.RemoveAll(path)
		return "", "", 0, "", 0, fmt.Errorf("error creating export for volume: %v", err)
	}

	return server, path, 0, block, exportId, nil
}

func (p *nfsProvisioner) validateOptions(options controller.VolumeOptions) (string, error) {
	gid := "none"
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "gid":
			if strings.ToLower(v) == "none" {
				gid = "none"
			} else if i, err := strconv.ParseUint(v, 10, 64); err == nil && i != 0 {
				gid = v
			} else {
				return "", fmt.Errorf("invalid value for parameter gid: %v. valid values are: 'none' or a non-zero integer", v)
			}
		default:
			return "", fmt.Errorf("invalid parameter: %q", k)
		}
	}

	// TODO implement options.ProvisionerSelector parsing
	// pv.Labels MUST be set to match claim.spec.selector
	// gid selector? with or without pv annotation?
	if options.Selector != nil {
		return "", fmt.Errorf("claim.Spec.Selector is not supported")
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(p.exportDir, &stat); err != nil {
		return "", fmt.Errorf("error calling statfs on %v: %v", p.exportDir, err)
	}
	capacity := options.Capacity.Value()
	available := int64(stat.Bavail) * stat.Bsize
	if capacity > available {
		return "", fmt.Errorf("insufficient available space %v bytes to satisfy claim for %v bytes", available, capacity)
	}

	return gid, nil
}

// getServer gets the server IP to put in a provisioned PV's spec.
func (p *nfsProvisioner) getServer() (string, error) {
	// Use either `hostname -i` or podIPEnv as the fallback server
	var fallbackServer string
	podIP := os.Getenv(p.podIPEnv)
	if podIP == "" {
		out, err := exec.Command("hostname", "-i").Output()
		if err != nil {
			return "", fmt.Errorf("hostname -i failed with error: %v, output: %s", err, out)
		}
		fallbackServer = string(out)
	} else {
		fallbackServer = podIP
	}

	// Try to use the service's cluster IP as the server if serviceEnv is
	// specified. If not, try to use nodeName if nodeEnv is specified (assume the
	// pod is using hostPort). If not again, use fallback here.
	serviceName := os.Getenv(p.serviceEnv)
	if serviceName == "" {
		nodeName := os.Getenv(p.nodeEnv)
		if nodeName == "" {
			glog.Infof("service env %s isn't set, using `hostname -i`/pod IP %s as server IP", p.serviceEnv, fallbackServer)
			return fallbackServer, nil
		}
		glog.Infof("service env %s isn't set and node env %s is, using node name %s as server IP", p.serviceEnv, p.nodeEnv, nodeName)
		return nodeName, nil
	}

	// From this point forward, rather than fallback & provision non-persistent
	// where persistent is expected, just return an error.
	namespace := os.Getenv(p.namespaceEnv)
	if namespace == "" {
		return "", fmt.Errorf("service env %s is set but namespace env %s isn't; no way to get the service cluster IP", p.serviceEnv, p.namespaceEnv)
	}
	service, err := p.client.Core().Services(namespace).Get(serviceName)
	if err != nil {
		return "", fmt.Errorf("error getting service %s=%s in namespace %s=%s", p.serviceEnv, serviceName, p.namespaceEnv, namespace)
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
		return "", fmt.Errorf("service %s=%s is not valid; check that it has for ports %v one endpoint, this pod's IP %v", p.serviceEnv, serviceName, expectedPorts, fallbackServer)
	}
	if service.Spec.ClusterIP == v1.ClusterIPNone {
		return "", fmt.Errorf("service %s=%s is valid but it doesn't have a cluster IP", p.serviceEnv, serviceName)
	}

	return service.Spec.ClusterIP, nil
}

// createDirectory creates the given directory in exportDir with appropriate
// permissions and ownership according to the given gid parameter string.
func (p *nfsProvisioner) createDirectory(directory, gid string) error {
	// TODO quotas
	path := fmt.Sprintf(p.exportDir+"%s", directory)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("error creating volume, the path already exists")
	}

	perm := os.FileMode(0777)
	if gid != "none" {
		// Execute permission is required for stat, which kubelet uses during unmount.
		perm = os.FileMode(0071)
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("error creating dir for volume: %v", err)
	}
	// Due to umask, need to chmod
	cmd := exec.Command("chmod", strconv.FormatInt(int64(perm), 8), path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return fmt.Errorf("chmod failed with error: %v, output: %s", err, out)
	}

	if gid != "none" {
		groupId, _ := strconv.ParseUint(gid, 10, 64)
		cmd = exec.Command("chgrp", strconv.FormatUint(groupId, 10), path)
		out, err = cmd.CombinedOutput()
		if err != nil {
			os.RemoveAll(path)
			return fmt.Errorf("chgrp failed with error: %v, output: %s", err, out)
		}
	}

	return nil
}

// createExport creates the export by adding a block to the appropriate config
// file and exporting it, using the appropriate method.
func (p *nfsProvisioner) createExport(directory string) (string, uint16, error) {
	path := fmt.Sprintf(p.exportDir+"%s", directory)

	exportId := p.generateExportId()
	exportIdStr := strconv.FormatUint(uint64(exportId), 10)

	config := p.exporter.GetConfig()
	block := p.exporter.CreateBlock(exportIdStr, path)

	// Add the export block to the config file
	if err := p.addToFile(config, block); err != nil {
		p.deleteExportId(exportId)
		return "", 0, fmt.Errorf("error adding export block %s to config %s: %v", block, config, err)
	}

	err := p.exporter.Export(path)
	if err != nil {
		p.deleteExportId(exportId)
		p.removeFromFile(config, block)
		return "", 0, fmt.Errorf("error exporting export block %s in config %s: %v", block, config, err)
	}

	return block, exportId, nil
}

// generateExportId generates a unique exportId to assign an export
func (p *nfsProvisioner) generateExportId() uint16 {
	p.mapMutex.Lock()
	id := uint16(1)
	for ; id <= math.MaxUint16; id++ {
		if _, ok := p.exportIds[id]; !ok {
			break
		}
	}
	p.exportIds[id] = true
	p.mapMutex.Unlock()
	return id
}

func (p *nfsProvisioner) deleteExportId(exportId uint16) {
	p.mapMutex.Lock()
	delete(p.exportIds, exportId)
	p.mapMutex.Unlock()
}

func (p *nfsProvisioner) addToFile(path string, toAdd string) error {
	p.fileMutex.Lock()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		p.fileMutex.Unlock()
		return err
	}
	defer file.Close()

	if _, err = file.WriteString(toAdd); err != nil {
		p.fileMutex.Unlock()
		return err
	}
	file.Sync()

	p.fileMutex.Unlock()
	return nil
}

func (p *nfsProvisioner) removeFromFile(path string, toRemove string) error {
	p.fileMutex.Lock()

	read, err := ioutil.ReadFile(path)
	if err != nil {
		p.fileMutex.Unlock()
		return err
	}

	removed := strings.Replace(string(read), toRemove, "", -1)
	err = ioutil.WriteFile(path, []byte(removed), 0)
	if err != nil {
		p.fileMutex.Unlock()
		return err
	}

	p.fileMutex.Unlock()
	return nil
}

type exporter interface {
	GetConfig() string
	GetConfigExportIds() (map[uint16]bool, error)
	CreateBlock(string, string) string
	Export(string) error
	Unexport(*v1.PersistentVolume) error
}

type ganeshaExporter struct {
	ganeshaConfig string
}

var _ exporter = &ganeshaExporter{}

func (e *ganeshaExporter) GetConfig() string {
	return e.ganeshaConfig
}

func (e *ganeshaExporter) GetConfigExportIds() (map[uint16]bool, error) {
	return getConfigExportIds(e.GetConfig(), regexp.MustCompile("Export_Id = ([0-9]+);"))
}

// CreateBlock creates the text block to add to the ganesha config file.
func (e *ganeshaExporter) CreateBlock(exportId, path string) string {
	return "\nEXPORT\n{\n" +
		"\tExport_Id = " + exportId + ";\n" +
		"\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tAccess_Type = RW;\n" +
		"\tSquash = root_id_squash;\n" +
		"\tSecType = sys;\n" +
		"\tFilesystem_id = " + exportId + "." + exportId + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"
}

// Export exports the given directory using NFS Ganesha, assuming it is running
// and can be connected to using D-Bus.
func (e *ganeshaExporter) Export(path string) error {
	// Call AddExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.AddExport", 0, e.ganeshaConfig, fmt.Sprintf("export(path = %s)", path))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.AddExport: %v", call.Err)
	}

	return nil
}

type kernelExporter struct {
}

var _ exporter = &kernelExporter{}

func (e *kernelExporter) GetConfig() string {
	return "/etc/exports"
}

func (e *kernelExporter) GetConfigExportIds() (map[uint16]bool, error) {
	return getConfigExportIds(e.GetConfig(), regexp.MustCompile("fsid=([0-9]+)"))
}

// CreateBlock creates the text block to add to the /etc/exports file.
func (e *kernelExporter) CreateBlock(exportId, path string) string {
	return "\n" + path + " *(rw,insecure,root_squash,fsid=" + exportId + ")\n"
}

// Export exports all directories listed in /etc/exports
func (e *kernelExporter) Export(_ string) error {
	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
}

// getConfigExportIds populates the exportIds map with pre-existing exportIds
// found in the given config file. Takes as argument the regex it should use to
// find each exportId in the file i.e. Export_Id or fsid.
func getConfigExportIds(config string, re *regexp.Regexp) (map[uint16]bool, error) {
	exportIds := map[uint16]bool{}

	digitsRe := "([0-9]+)"
	if !strings.Contains(re.String(), digitsRe) {
		return exportIds, fmt.Errorf("regexp %s doesn't contain digits submatch %s", re.String(), digitsRe)
	}

	read, err := ioutil.ReadFile(config)
	if err != nil {
		return exportIds, err
	}

	allMatches := re.FindAllSubmatch(read, -1)
	for _, match := range allMatches {
		digits := match[1]
		if id, err := strconv.ParseUint(string(digits), 10, 16); err == nil {
			exportIds[uint16(id)] = true
		}
	}

	return exportIds, nil
}
