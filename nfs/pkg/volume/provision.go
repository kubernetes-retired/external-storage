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
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
)

const (
	// Name of the file where an nfsProvisioner will store its identity
	identityFile = "nfs-provisioner.identity"

	// are we allowed to set this? else make up our own
	annCreatedBy = "kubernetes.io/createdby"
	createdBy    = "nfs-dynamic-provisioner"

	// A PV annotation for the entire ganesha EXPORT block or /etc/exports
	// block, needed for deletion.
	annExportBlock = "EXPORT_block"
	// A PV annotation for the exportID of this PV's backing ganesha/kernel export
	// , needed for ganesha deletion and used for deleting the entry in exportIDs
	// map so the id can be reassigned.
	annExportID = "Export_Id"

	// A PV annotation for the project quota info block, needed for quota
	// deletion.
	annProjectBlock = "Project_block"
	// A PV annotation for the project quota id, needed for quota deletion
	annProjectID = "Project_Id"

	// VolumeGidAnnotationKey is the key of the annotation on the PersistentVolume
	// object that specifies a supplemental GID.
	VolumeGidAnnotationKey = "pv.beta.kubernetes.io/gid"

	// MountOptionAnnotation is the annotation on a PV object that specifies a
	// comma separated list of mount options
	MountOptionAnnotation = "volume.beta.kubernetes.io/mount-options"

	// A PV annotation for the identity of the nfsProvisioner that provisioned it
	annProvisionerID = "Provisioner_Id"

	podIPEnv     = "POD_IP"
	serviceEnv   = "SERVICE_NAME"
	namespaceEnv = "POD_NAMESPACE"
	nodeEnv      = "NODE_NAME"
)

// NewNFSProvisioner creates a Provisioner that provisions NFS PVs backed by
// the given directory.
func NewNFSProvisioner(exportDir string, client kubernetes.Interface, outOfCluster bool, useGanesha bool, ganeshaConfig string, enableXfsQuota bool, serverHostname string, maxExports int, exportSubnet string) controller.Provisioner {
	var exp exporter
	if useGanesha {
		exp = newGaneshaExporter(ganeshaConfig)
	} else {
		exp = newKernelExporter()
	}
	var quotaer quotaer
	var err error
	if enableXfsQuota {
		quotaer, err = newXfsQuotaer(exportDir)
		if err != nil {
			glog.Fatalf("Error creating xfs quotaer! %v", err)
		}
	} else {
		quotaer = newDummyQuotaer()
	}
	return newNFSProvisionerInternal(exportDir, client, outOfCluster, exp, quotaer, serverHostname, maxExports, exportSubnet)
}

func newNFSProvisionerInternal(exportDir string, client kubernetes.Interface, outOfCluster bool, exporter exporter, quotaer quotaer, serverHostname string, maxExports int, exportSubnet string) *nfsProvisioner {
	if _, err := os.Stat(exportDir); os.IsNotExist(err) {
		glog.Fatalf("exportDir %s does not exist!", exportDir)
	}

	var identity types.UID
	identityPath := path.Join(exportDir, identityFile)
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		identity = uuid.NewUUID()
		err := ioutil.WriteFile(identityPath, []byte(identity), 0600)
		if err != nil {
			glog.Fatalf("Error writing identity file %s! %v", identityPath, err)
		}
	} else {
		read, err := ioutil.ReadFile(identityPath)
		if err != nil {
			glog.Fatalf("Error reading identity file %s! %v", identityPath, err)
		}
		identity = types.UID(strings.TrimSpace(string(read)))
	}

	provisioner := &nfsProvisioner{
		exportDir:      exportDir,
		client:         client,
		outOfCluster:   outOfCluster,
		exporter:       exporter,
		quotaer:        quotaer,
		serverHostname: serverHostname,
		maxExports:     maxExports,
		exportSubnet:   exportSubnet,
		identity:       identity,
		podIPEnv:       podIPEnv,
		serviceEnv:     serviceEnv,
		namespaceEnv:   namespaceEnv,
		nodeEnv:        nodeEnv,
	}

	return provisioner
}

type nfsProvisioner struct {
	// The directory to create PV-backing directories in
	exportDir string

	// Client, needed for getting a service cluster IP to put as the NFS server of
	// provisioned PVs
	client kubernetes.Interface

	// Whether the provisioner is running out of cluster and so cannot rely on
	// the existence of any of the pod, service, namespace, node env variables.
	outOfCluster bool

	// The exporter to use for exporting NFS shares
	exporter exporter

	// The quotaer to use for setting per-share/directory/project quotas
	quotaer quotaer

	// The hostname for the NFS server to export from. Only applicable when
	// running as a Docker container
	serverHostname string

	// The maximum number of volumes to be exported by the provisioner
	maxExports int

	// Subnet for NFS export to allow mount only from
	exportSubnet string

	// Identity of this nfsProvisioner, generated & persisted to exportDir or
	// recovered from there. Used to mark provisioned PVs
	identity types.UID

	// Environment variables the provisioner pod needs valid values for in order to
	// put a service cluster IP as the server of provisioned NFS PVs, passed in
	// via downward API. If serviceEnv is set, namespaceEnv must be too.
	podIPEnv     string
	serviceEnv   string
	namespaceEnv string
	nodeEnv      string
}

var _ controller.Provisioner = &nfsProvisioner{}
var _ controller.Qualifier = &nfsProvisioner{}

// ShouldProvision returns whether provisioning should be attempted for the given
// claim.
func (p *nfsProvisioner) ShouldProvision(claim *v1.PersistentVolumeClaim) bool {
	// As long as the export limit has not been reached we're ok to provision
	ok := p.checkExportLimit()
	if !ok {
		glog.Infof("export limit reached. skipping claim %s/%s", claim.Namespace, claim.Name)
	}
	return ok
}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume.
func (p *nfsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	volume, err := p.createVolume(options)
	if err != nil {
		return nil, err
	}

	annotations := make(map[string]string)
	annotations[annCreatedBy] = createdBy
	annotations[annExportBlock] = volume.exportBlock
	annotations[annExportID] = strconv.FormatUint(uint64(volume.exportID), 10)
	annotations[annProjectBlock] = volume.projectBlock
	annotations[annProjectID] = strconv.FormatUint(uint64(volume.projectID), 10)
	if volume.supGroup != 0 {
		annotations[VolumeGidAnnotationKey] = strconv.FormatUint(volume.supGroup, 10)
	}
	if volume.mountOptions != "" {
		annotations[MountOptionAnnotation] = volume.mountOptions
	}
	annotations[annProvisionerID] = string(p.identity)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        options.PVName,
			Labels:      map[string]string{},
			Annotations: annotations,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   volume.server,
					Path:     volume.path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

type volume struct {
	server       string
	path         string
	exportBlock  string
	exportID     uint16
	projectBlock string
	projectID    uint16
	supGroup     uint64
	mountOptions string
}

// createVolume creates a volume i.e. the storage asset. It creates a unique
// directory under /export and exports it. Returns the server IP, the path, a
// zero/non-zero supplemental group, the block it added to either the ganesha
// config or /etc/exports, and the exportID
// TODO return values
func (p *nfsProvisioner) createVolume(options controller.VolumeOptions) (volume, error) {
	gid, rootSquash, mountOptions, err := p.validateOptions(options)
	if err != nil {
		return volume{}, fmt.Errorf("error validating options for volume: %v", err)
	}

	server, err := p.getServer()
	if err != nil {
		return volume{}, fmt.Errorf("error getting NFS server IP for volume: %v", err)
	}

	if ok := p.checkExportLimit(); !ok {
		return volume{}, &controller.IgnoredError{Reason: fmt.Sprintf("export limit of %v has been reached", p.maxExports)}
	}

	path := path.Join(p.exportDir, options.PVName)

	err = p.createDirectory(options.PVName, gid)
	if err != nil {
		return volume{}, fmt.Errorf("error creating directory for volume: %v", err)
	}

	exportBlock, exportID, err := p.createExport(options.PVName, rootSquash)
	if err != nil {
		os.RemoveAll(path)
		return volume{}, fmt.Errorf("error creating export for volume: %v", err)
	}

	projectBlock, projectID, err := p.createQuota(options.PVName, options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)])
	if err != nil {
		os.RemoveAll(path)
		return volume{}, fmt.Errorf("error creating quota for volume: %v", err)
	}

	return volume{
		server:       server,
		path:         path,
		exportBlock:  exportBlock,
		exportID:     exportID,
		projectBlock: projectBlock,
		projectID:    projectID,
		supGroup:     0,
		mountOptions: mountOptions,
	}, nil
}

func (p *nfsProvisioner) validateOptions(options controller.VolumeOptions) (string, bool, string, error) {
	gid := "none"
	rootSquash := false
	mountOptions := ""
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "gid":
			if strings.ToLower(v) == "none" {
				gid = "none"
			} else if i, err := strconv.ParseUint(v, 10, 64); err == nil && i != 0 {
				gid = v
			} else {
				return "", false, "", fmt.Errorf("invalid value for parameter gid: %v. valid values are: 'none' or a non-zero integer", v)
			}
		case "rootsquash":
			var err error
			rootSquash, err = strconv.ParseBool(v)
			if err != nil {
				return "", false, "", fmt.Errorf("invalid value for parameter rootSquash: %v. valid values are: 'true' or 'false'", v)
			}
		case "mountoptions":
			mountOptions = v
		default:
			return "", false, "", fmt.Errorf("invalid parameter: %q", k)
		}
	}

	// TODO implement options.ProvisionerSelector parsing
	// pv.Labels MUST be set to match claim.spec.selector
	// gid selector? with or without pv annotation?
	if options.PVC.Spec.Selector != nil {
		return "", false, "", fmt.Errorf("claim.Spec.Selector is not supported")
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(p.exportDir, &stat); err != nil {
		return "", false, "", fmt.Errorf("error calling statfs on %v: %v", p.exportDir, err)
	}
	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	requestBytes := capacity.Value()
	available := int64(stat.Bavail) * int64(stat.Bsize)
	if requestBytes > available {
		return "", false, "", fmt.Errorf("insufficient available space %v bytes to satisfy claim for %v bytes", available, requestBytes)
	}

	return gid, rootSquash, mountOptions, nil
}

// getServer gets the server IP to put in a provisioned PV's spec.
func (p *nfsProvisioner) getServer() (string, error) {
	if p.outOfCluster {
		if p.serverHostname != "" {
			return p.serverHostname, nil
		}
		// TODO make this better
		out, err := exec.Command("hostname", "-i").Output()
		if err != nil {
			return "", fmt.Errorf("hostname -i failed with error: %v, output: %s", err, out)
		}
		addresses := strings.Fields(string(out))
		if len(addresses) > 0 {
			return addresses[0], nil
		}
		return "", fmt.Errorf("hostname -i had bad output %s, no address to use", string(out))
	}

	nodeName := os.Getenv(p.nodeEnv)
	if nodeName != "" {
		glog.Infof("using node name %s=%s as NFS server IP", p.nodeEnv, nodeName)
		return nodeName, nil
	}

	podIP := os.Getenv(p.podIPEnv)
	if podIP == "" {
		return "", fmt.Errorf("pod IP env %s must be set even if intent is to use service cluster IP as NFS server IP", p.podIPEnv)
	}

	serviceName := os.Getenv(p.serviceEnv)
	if serviceName == "" {
		glog.Infof("using potentially unstable pod IP %s=%s as NFS server IP (because neither service env %s nor node env %s are set)", p.podIPEnv, podIP, p.serviceEnv, p.nodeEnv)
		return podIP, nil
	}

	// Service env was set, now find and validate it
	namespace := os.Getenv(p.namespaceEnv)
	if namespace == "" {
		return "", fmt.Errorf("service env %s is set but namespace env %s isn't; no way to get the service cluster IP", p.serviceEnv, p.namespaceEnv)
	}
	service, err := p.client.CoreV1().Services(namespace).Get(serviceName, metav1.GetOptions{})
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
		{2049, v1.ProtocolTCP}:  true,
		{20048, v1.ProtocolTCP}: true,
		{111, v1.ProtocolUDP}:   true,
		{111, v1.ProtocolTCP}:   true,
	}
	endpoints, err := p.client.CoreV1().Endpoints(namespace).Get(serviceName, metav1.GetOptions{})
	for _, subset := range endpoints.Subsets {
		// One service can't have multiple nfs-provisioner endpoints. If it had, kubernetes would round-robin
		// the request which would probably go to the wrong instance.
		if len(subset.Addresses) != 1 {
			continue
		}
		if subset.Addresses[0].IP != podIP {
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
		return "", fmt.Errorf("service %s=%s is not valid; check that it has for ports %v exactly one endpoint, this pod's IP %s=%s", p.serviceEnv, serviceName, expectedPorts, p.podIPEnv, podIP)
	}
	if service.Spec.ClusterIP == v1.ClusterIPNone {
		return "", fmt.Errorf("service %s=%s is valid but it doesn't have a cluster IP", p.serviceEnv, serviceName)
	}

	glog.Infof("using service %s=%s cluster IP %s as NFS server IP", p.serviceEnv, serviceName, service.Spec.ClusterIP)
	return service.Spec.ClusterIP, nil
}

func (p *nfsProvisioner) checkExportLimit() bool {
	return p.exporter.CanExport(p.maxExports)
}

// createDirectory creates the given directory in exportDir with appropriate
// permissions and ownership according to the given gid parameter string.
func (p *nfsProvisioner) createDirectory(directory, gid string) error {
	// TODO quotas
	path := path.Join(p.exportDir, directory)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("the path already exists")
	}

	perm := os.FileMode(0777 | os.ModeSetgid)
	if gid != "none" {
		// Execute permission is required for stat, which kubelet uses during unmount.
		perm = os.FileMode(0071 | os.ModeSetgid)
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	// Due to umask, need to chmod
	if err := os.Chmod(path, perm); err != nil {
		os.RemoveAll(path)
		return err
	}

	if gid != "none" {
		groupID, err := strconv.ParseUint(gid, 10, 64)
		if err != nil {
			os.RemoveAll(path)
			return fmt.Errorf("strconv.ParseUint failed with error: %v", err)
		}
		cmd := exec.Command("chgrp", strconv.FormatUint(groupID, 10), path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			os.RemoveAll(path)
			return fmt.Errorf("chgrp failed with error: %v, output: %s", err, out)
		}
	}

	return nil
}

// createExport creates the export by adding a block to the appropriate config
// file and exporting it
func (p *nfsProvisioner) createExport(directory string, rootSquash bool) (string, uint16, error) {
	path := path.Join(p.exportDir, directory)

	block, exportID, err := p.exporter.AddExportBlock(path, rootSquash, p.exportSubnet)
	if err != nil {
		return "", 0, fmt.Errorf("error adding export block for path %s: %v", path, err)
	}

	err = p.exporter.Export(path)
	if err != nil {
		p.exporter.RemoveExportBlock(block, exportID)
		return "", 0, fmt.Errorf("error exporting export block %s: %v", block, err)
	}

	return block, exportID, nil
}

// createQuota creates a quota for the directory by adding a project to
// represent the directory and setting a quota on it
func (p *nfsProvisioner) createQuota(directory string, capacity resource.Quantity) (string, uint16, error) {
	path := path.Join(p.exportDir, directory)

	limit := strconv.FormatInt(capacity.Value(), 10)

	block, projectID, err := p.quotaer.AddProject(path, limit)
	if err != nil {
		return "", 0, fmt.Errorf("error adding project for path %s: %v", path, err)
	}

	err = p.quotaer.SetQuota(projectID, path, limit)
	if err != nil {
		p.quotaer.RemoveProject(block, projectID)
		return "", 0, fmt.Errorf("error setting quota for path %s: %v", path, err)
	}

	return block, projectID, nil
}
