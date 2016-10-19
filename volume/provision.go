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
	"encoding/json"
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
	"github.com/openshift/origin/pkg/security/uid"
	"github.com/wongma7/nfs-provisioner/controller"
	"k8s.io/client-go/1.4/dynamic"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api/unversioned"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/1.4/pkg/runtime"
)

const (
	// ValidatedPSPAnnotation is the annotation on the pod object that specifies
	// the name of the PSP the pod validated against, if any.
	ValidatedPSPAnnotation = "kubernetes.io/psp"

	// ValidatedSCCAnnotation is the annotation on the pod object that specifies
	// the name of the SCC the pod validated against, if any.
	ValidatedSCCAnnotation = "openshift.io/scc"
	UIDRangeAnnotation     = "openshift.io/sa.scc.uid-range"
	// SupplementalGroupsAnnotation contains a comma delimited list of allocated supplemental groups
	// for the namespace.  Groups are in the form of a Block which supports {start}/{length} or {start}-{end}
	SupplementalGroupsAnnotation = "openshift.io/sa.scc.supplemental-groups"

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

	podIPEnv     = "MY_POD_IP"
	serviceEnv   = "MY_SERVICE_NAME"
	namespaceEnv = "MY_POD_NAMESPACE"
)

func NewNFSProvisioner(exportDir string, client kubernetes.Interface, dynamicClient *dynamic.Client, useGanesha bool, ganeshaConfig string) controller.Provisioner {
	return newProvisionerInternal(exportDir, client, dynamicClient, useGanesha, ganeshaConfig)
}

func newProvisionerInternal(exportDir string, client kubernetes.Interface, dynamicClient *dynamic.Client, useGanesha bool, ganeshaConfig string) *nfsProvisioner {
	provisioner := &nfsProvisioner{
		exportDir:     exportDir,
		client:        client,
		useGanesha:    useGanesha,
		ganeshaConfig: ganeshaConfig,
		mapMutex:      &sync.Mutex{},
		fileMutex:     &sync.Mutex{},
		podIPEnv:      podIPEnv,
		serviceEnv:    serviceEnv,
		namespaceEnv:  namespaceEnv,
	}

	configPath := ganeshaConfig
	re := regexp.MustCompile("Export_Id = ([0-9]+);")
	if !useGanesha {
		configPath = "/etc/exports"
		re = regexp.MustCompile("fsid=([0-9]+)")
	}
	var err error
	provisioner.exportIds, err = getExportIds(configPath, re)
	if err != nil {
		glog.Errorf("error while populating exportIds map, there may be errors exporting later if exportIds are reused: %v", err)
	}

	// provisioner.ranges = getSupplementalGroupsRanges(client, dynamicClient, "/podinfo/annotations", os.Getenv(provisioner.namespaceEnv))

	return provisioner
}

type nfsProvisioner struct {
	// The directory to create PV-backing directories in
	exportDir string

	// Client, needed for getting a service cluster IP to put as the NFS server of
	// provisioned PVs
	client kubernetes.Interface

	// Whether to use NFS Ganesha (D-Bus method calls) or kernel NFS server
	// (exportfs)
	useGanesha bool

	// The path of the NFS Ganesha configuration file
	ganeshaConfig string

	// Map to track used exportIds. Each ganesha export needs a unique Export_Id,
	// and both ganesha and kernel exports need a unique fsid. So we simply assign
	// each export an exportId and use it as both Export_id and fsid.
	exportIds map[uint16]bool

	// Lock for accessing exportIds
	mapMutex *sync.Mutex

	// Lock for writing to the ganesha config or /etc/exports file
	fileMutex *sync.Mutex

	// Ranges of gids to assign to PV's
	ranges []v1beta1.IDRange

	// Environment variables the provisioner pod needs valid values for in order to
	// put a service cluster IP as the server of provisioned NFS PVs, passed in
	// via downward API. If serviceEnv is set, namespaceEnv must be too.
	podIPEnv     string
	serviceEnv   string
	namespaceEnv string
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
	gid := "none"
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
		case "gid":
			if strings.ToLower(v) == "none" {
				gid = "none"
			} else if i, err := strconv.ParseUint(v, 10, 64); err == nil && i != 0 {
				gid = v
			} else {
				return "", "", 0, "", 0, fmt.Errorf("invalid value for parameter gid: %v. valid values are: 'none' or a non-zero integer", v)
			}
		default:
			return "", "", 0, "", 0, fmt.Errorf("invalid parameter: %q", k)
		}
	}

	// TODO implement options.ProvisionerSelector parsing
	// TODO pv.Labels MUST be set to match claim.spec.selector
	if options.Selector != nil {
		return "", "", 0, "", 0, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(p.exportDir, &stat); err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error calling statfs on %v: %v", p.exportDir, err)
	}
	capacity := options.Capacity.Value()
	available := int64(stat.Bavail) * stat.Bsize
	if capacity > available {
		return "", "", 0, "", 0, fmt.Errorf("insufficient available space %v bytes to satisfy claim for %v bytes", available, capacity)
	}

	server, err := p.getServer()
	if err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error getting NFS server IP for volume: %v", err)
	}

	path := fmt.Sprintf(p.exportDir+"%s", options.PVName)

	err = p.createDirectory(path, gid)
	if err != nil {
		return "", "", 0, "", 0, fmt.Errorf("error creating directory for volume: %v", err)
	}

	block, exportId, err := p.createExport(path)
	if err != nil {
		os.RemoveAll(path)
		return "", "", 0, "", 0, fmt.Errorf("error creating export for volume: %v", err)
	}

	return server, path, 0, block, exportId, nil
}

// createDirectory creates the directory at the given path with appropriate
// permissions and ownership according to the given gid parameter string.
func (p *nfsProvisioner) createDirectory(path, gid string) error {
	// TODO quota, something better than just directories
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
func (p *nfsProvisioner) createExport(path string) (string, uint16, error) {
	var block string
	var exportId uint16
	var config string
	if p.useGanesha {
		block, exportId = p.generateGaneshaBlock(path)
		config = p.ganeshaConfig
	} else {
		block, exportId = p.generateKernelBlock(path)
		config = "/etc/exports"
	}

	// Add the export block to the config file
	if err := p.addToFile(config, block); err != nil {
		return "", 0, fmt.Errorf("error adding export block to the config file %s: %v", config, err)
	}

	var err error
	if p.useGanesha {
		err = p.exportGanesha(path)
	} else {
		err = p.exportKernel()
	}
	if err != nil {
		p.removeFromFile(config, block)
		return "", 0, err
	}

	return block, exportId, nil
}

// generateGaneshaBlock generates the block to add to the ganesha config file.
// Returns the block plus the exportId.
func (p *nfsProvisioner) generateGaneshaBlock(path string) (string, uint16) {
	exportId := p.generateExportId()
	exportIdStr := strconv.FormatUint(uint64(exportId), 10)

	block := "\nEXPORT\n{\n" +
		"\tExport_Id = " + exportIdStr + ";\n" +
		"\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tAccess_Type = RW;\n" +
		"\tSquash = root_id_squash;\n" +
		"\tSecType = sys;\n" +
		"\tFilesystem_id = " + exportIdStr + "." + exportIdStr + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"

	return block, exportId
}

// generateKernelBlock generates the block to add to the /etc/exports file.
// Returns the block plus the exportId.
func (p *nfsProvisioner) generateKernelBlock(path string) (string, uint16) {
	exportId := p.generateExportId()
	exportIdStr := strconv.FormatUint(uint64(exportId), 10)

	block := "\n" + path + " *(rw,insecure,root_squash,fsid=" + exportIdStr + ")\n"

	return block, exportId
}

// exportGanesha exports the given directory using NFS Ganesha, assuming it is
// running and can be connected to using D-Bus.
func (p *nfsProvisioner) exportGanesha(path string) error {
	// Call AddExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.AddExport", 0, p.ganeshaConfig, fmt.Sprintf("export(path = %s)", path))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.AddExport: %v", call.Err)
	}

	return nil
}

// exportKernel exports all directories listed in /etc/exports
func (p *nfsProvisioner) exportKernel() error {
	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
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

// getServer gets the server IP to put in a provisioned PV's spec.
func (p *nfsProvisioner) getServer() (string, error) {
	// Use either `hostname -i` or podIPEnv as the fallback server
	var fallbackServer string
	podIP := os.Getenv(p.podIPEnv)
	if podIP == "" {
		glog.Infof("pod IP env %s isn't set or provisioner isn't running as a pod", p.podIPEnv)
		out, err := exec.Command("hostname", "-i").Output()
		if err != nil {
			return "", fmt.Errorf("hostname -i failed with error: %v, output: %s", err, out)
		}
		fallbackServer = string(out)
	} else {
		fallbackServer = podIP
	}

	// Try to use the service's cluster IP as the server if serviceEnv is
	// specified. Otherwise, use fallback here.
	serviceName := os.Getenv(p.serviceEnv)
	if serviceName == "" {
		glog.Infof("service env %s isn't set, falling back to using `hostname -i` or pod IP as server IP", p.serviceEnv)
		return fallbackServer, nil
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

// getExportIds populates the exportIds map with pre-existing exportIds found in
// the given config file. Takes as argument the regex it should use to find each
// exportId in the file i.e. Export_Id or fsid.
func getExportIds(configPath string, re *regexp.Regexp) (map[uint16]bool, error) {
	exportIds := map[uint16]bool{}

	digitsRe := "([0-9]+)"
	if !strings.Contains(re.String(), digitsRe) {
		return exportIds, fmt.Errorf("regexp %s doesn't contain digits submatch %s", re.String(), digitsRe)
	}

	read, err := ioutil.ReadFile(configPath)
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

// getSupplementalGroupsRanges gets the ranges of SupplementalGroups the
// provisioner pod is allowed to run as. Range rules can be imposed by a PSP,
// SCC or namespace (latter two in openshift only).
func getSupplementalGroupsRanges(client kubernetes.Interface, dynamicClient *dynamic.Client, downwardAnnotationsFile string, namespace string) []v1beta1.IDRange {
	sccName, err := getPodAnnotation(downwardAnnotationsFile, ValidatedSCCAnnotation)
	if err != nil {
		glog.Errorf("error getting pod annotation %s: %v", ValidatedSCCAnnotation, err)
	} else if sccName != "" {
		sccResource := unversioned.APIResource{Name: "securitycontextconstraints", Namespaced: false, Kind: "SecurityContextConstraints"}
		sccClient := dynamicClient.Resource(&sccResource, "")
		scc, err := sccClient.Get(sccName)
		if err != nil {
			glog.Errorf("error getting provisioner pod's scc: %v", err)
		} else {
			ranges := getSCCSupplementalGroups(scc)
			if ranges != nil && len(ranges) > 0 {
				return ranges
			}
		}
	}

	pspName, err := getPodAnnotation(downwardAnnotationsFile, ValidatedPSPAnnotation)
	if err != nil {
		glog.Errorf("error getting pod annotation %s: %v", ValidatedPSPAnnotation, err)
	} else if pspName != "" {
		psp, err := client.Extensions().PodSecurityPolicies().Get(pspName)
		if err != nil {
			glog.Errorf("error getting provisioner pod's psp: %v", err)
		} else {
			ranges := getPSPSupplementalGroups(psp)
			if ranges != nil && len(ranges) > 0 {
				return ranges
			}
		}
	}

	ns, err := client.Core().Namespaces().Get(namespace)
	if err != nil {
		glog.Errorf("error getting namespace %s: %v", namespace, err)
	} else {
		ranges, err := getPreallocatedSupplementalGroups(ns)
		if err != nil {
			glog.Errorf("error getting preallcoated supplemental groups: %v", err)
		} else if ranges != nil && len(ranges) > 0 {
			return ranges
		}
	}

	return []v1beta1.IDRange{{Min: int64(1), Max: int64(65533)}}
}

// getPodAnnotation returns the value of the given annotation on the provisioner
// pod or an empty string if the annotation doesn't exist.
func getPodAnnotation(downwardAnnotationsFile string, annotation string) (string, error) {
	read, err := ioutil.ReadFile(downwardAnnotationsFile)
	if err != nil {
		return "", fmt.Errorf("error reading downward API annotations volume: %v", err)
	}
	re := regexp.MustCompile(annotation + "=\".*\"$")
	line := re.Find(read)
	if line == nil {
		return "", nil
	}
	re = regexp.MustCompile("\"(.*?)\"")
	value := re.FindStringSubmatch(string(line))[1]
	return value, nil
}

// getPSPSupplementalGroups returns the SupplementalGroup Ranges of the PSP or
// nil if the PSP doesn't impose gid range rules.
func getPSPSupplementalGroups(psp *v1beta1.PodSecurityPolicy) []v1beta1.IDRange {
	if psp == nil {
		return nil
	}
	if psp.Spec.SupplementalGroups.Rule != v1beta1.SupplementalGroupsStrategyMustRunAs {
		return nil
	}
	return psp.Spec.SupplementalGroups.Ranges
}

// TODO "Type" vs "Rule"
// SupplementalGroupsStrategyOptions defines the strategy type and options used to create the strategy.
type SupplementalGroupsStrategyOptions struct {
	// Type is the strategy that will dictate what supplemental groups is used in the SecurityContext.
	Type v1beta1.SupplementalGroupsStrategyType `json:"type,omitempty" protobuf:"bytes,1,opt,name=type,casttype=SupplementalGroupsStrategyType"`
	// Ranges are the allowed ranges of supplemental groups.  If you would like to force a single
	// supplemental group then supply a single range with the same start and end.
	Ranges []v1beta1.IDRange `json:"ranges,omitempty" protobuf:"bytes,2,rep,name=ranges"`
}

// getSCCSupplementalGroups returns the SupplementalGroup Ranges of the SCC or
// nil if the SCC doesn't impose gid range rules.
func getSCCSupplementalGroups(scc *runtime.Unstructured) []v1beta1.IDRange {
	if scc == nil {
		return nil
	}
	data, _ := scc.MarshalJSON()
	var v map[string]interface{}
	_ = json.Unmarshal(data, &v)
	if _, ok := v["supplementalGroups"]; !ok {
		return nil
	}
	data, _ = json.Marshal(v["supplementalGroups"])
	supplementalGroups := SupplementalGroupsStrategyOptions{}
	_ = json.Unmarshal(data, &supplementalGroups)
	if supplementalGroups.Type != v1beta1.SupplementalGroupsStrategyMustRunAs {
		return nil
	}
	return supplementalGroups.Ranges
}

// getSupplementalGroupsAnnotation provides a backwards compatible way to get supplemental groups
// annotations from a namespace by looking for SupplementalGroupsAnnotation and falling back to
// UIDRangeAnnotation if it is not found.
// openshift/origin pkg/security/scc/matcher.go
func getSupplementalGroupsAnnotation(ns *v1.Namespace) (string, error) {
	groups, ok := ns.Annotations[SupplementalGroupsAnnotation]
	if !ok {
		glog.V(4).Infof("unable to find supplemental group annotation %s falling back to %s", SupplementalGroupsAnnotation, UIDRangeAnnotation)

		groups, ok = ns.Annotations[UIDRangeAnnotation]
		if !ok {
			return "", fmt.Errorf("unable to find supplemental group or uid annotation for namespace %s", ns.Name)
		}
	}

	if len(groups) == 0 {
		return "", fmt.Errorf("unable to find groups using %s and %s annotations", SupplementalGroupsAnnotation, UIDRangeAnnotation)
	}
	return groups, nil
}

// getPreallocatedSupplementalGroups gets the annotated value from the namespace.
// openshift/origin pkg/security/scc/matcher.go
func getPreallocatedSupplementalGroups(ns *v1.Namespace) ([]v1beta1.IDRange, error) {
	groups, err := getSupplementalGroupsAnnotation(ns)
	if err != nil {
		return nil, err
	}
	glog.V(4).Infof("got preallocated value for groups: %s in namespace %s", groups, ns.Name)

	blocks, err := parseSupplementalGroupAnnotation(groups)
	if err != nil {
		return nil, err
	}

	idRanges := []v1beta1.IDRange{}
	for _, block := range blocks {
		rng := v1beta1.IDRange{
			Min: int64(block.Start),
			Max: int64(block.End),
		}
		idRanges = append(idRanges, rng)
	}
	return idRanges, nil
}

// parseSupplementalGroupAnnotation parses the group annotation into blocks.
// openshift/origin pkg/security/scc/matcher.go
func parseSupplementalGroupAnnotation(groups string) ([]uid.Block, error) {
	blocks := []uid.Block{}
	segments := strings.Split(groups, ",")
	for _, segment := range segments {
		block, err := uid.ParseBlock(segment)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks parsed from annotation %s", groups)
	}
	return blocks, nil
}
