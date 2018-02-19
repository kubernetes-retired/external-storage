/*
Copyright 2018 The Kubernetes Authors.

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
	"flag"
	"fmt"
	"os"
	"strconv"
	dstrings "strings"

	"github.com/golang/glog"
	gcli "github.com/heketi/heketi/client/api/go-client"
	gapi "github.com/heketi/heketi/pkg/glusterfs/api"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/gidallocator"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/pborman/uuid"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	provisionerName    = "gluster.org/glusterfile"
	provisionerNameKey = "PROVISIONER_NAME"
	descAnn            = "Gluster-external: Dynamically provisioned PV"
	restStr            = "server"
	dynamicEpSvcPrefix = "glusterfile-dynamic-"
	replicaCount       = 3
	secretKeyName      = "key" // key name used in secret
	volPrefix          = "vol_"
	mountStr           = "auto_unmount"
	glusterTypeAnn     = "gluster.org/type"
	heketiVolIDAnn     = "gluster.org/heketi-volume-id"
	gidAnn             = "pv.beta.kubernetes.io/gid"
)

type glusterfileProvisioner struct {
	client   kubernetes.Interface
	identity string
	provisionerConfig
	allocator gidallocator.Allocator
	options   controller.VolumeOptions
}

type provisionerConfig struct {
	url              string
	user             string
	userKey          string
	secretNamespace  string
	secretName       string
	secretValue      string
	clusterID        string
	gidMin           int
	gidMax           int
	volumeType       gapi.VolumeDurabilityInfo
	volumeOptions    []string
	volumeNamePrefix string
}

//NewglusterfileProvisioner create a new provisioner.
func NewglusterfileProvisioner(client kubernetes.Interface, id string) controller.Provisioner {
	return &glusterfileProvisioner{
		client:    client,
		identity:  id,
		allocator: gidallocator.New(client),
	}
}

var _ controller.Provisioner = &glusterfileProvisioner{}

func (p *glusterfileProvisioner) GetAccessModes() []v1.PersistentVolumeAccessMode {
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteMany,
		v1.ReadOnlyMany,
		v1.ReadWriteOnce,
	}
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *glusterfileProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {

	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	if !util.AccessModesContainedInAll(p.GetAccessModes(), options.PVC.Spec.AccessModes) {
		return nil, fmt.Errorf("invalid AccessModes %v: only AccessModes %v are supported", options.PVC.Spec.AccessModes, p.GetAccessModes())
	}

	glog.V(1).Infof("VolumeOptions %v", options)
	p.options = options
	gidAllocate := true
	for k, v := range options.Parameters {
		switch dstrings.ToLower(k) {
		case "gidmin":
		// Let allocator handle
		case "gidmax":
		// Let allocator handle
		case "gidallocate":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("invalid value %s for parameter %s: %v", v, k, err)
			}
			gidAllocate = b
		}
	}

	var gid *int
	if gidAllocate {
		allocate, err := p.allocator.AllocateNext(options)
		if err != nil {
			return nil, fmt.Errorf("allocator error: %v", err)
		}
		gid = &allocate

	}

	cfg, parseErr := p.parseClassParameters(options.Parameters, p.client)

	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse storage class parameters: %v", parseErr)
	}

	glog.V(4).Infof("creating volume with configuration %+v", *cfg)

	modeAnn := "url:" + cfg.url + "," + "user:" + cfg.user + "," + "secret:" + cfg.secretName + "," + "secretnamespace:" + cfg.secretNamespace
	glog.V(1).Infof("Allocated GID %d for PVC %s", gid, options.PVC.Name)
	gidStr := strconv.FormatInt(int64(*gid), 10)

	volSize := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := volSize.Value()
	volszInt := int(util.RoundUpToGiB(volSizeBytes))

	glusterfs, sizeGiB, volID, err := p.CreateVolume(gid, cfg, volszInt)
	if err != nil {
		glog.Errorf("failed to create volume: %v", err)
		return nil, fmt.Errorf("failed to create volume: %v", err)
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				gidAnn:                   gidStr,
				glusterTypeAnn:           "file",
				"Description":            descAnn,
				heketiVolIDAnn:           volID,
				restStr:                  modeAnn,
				v1.MountOptionAnnotation: mountStr,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				Glusterfs: glusterfs,
			},
		},
	}
	glog.V(1).Infof("successfully created Gluster File volume %+v with size", pv.Spec.PersistentVolumeSource.Glusterfs, sizeGiB)
	return pv, nil
}

func (p *glusterfileProvisioner) CreateVolume(gid *int, config *provisionerConfig, sz int) (r *v1.GlusterfsVolumeSource, size int, volID string, err error) {
	var clusterIDs []string
	customVolumeName := ""

	glog.V(2).Infof("create volume of size %dGiB and configuration %+v", sz, config)

	if config.url == "" {
		glog.Errorf("REST server endpoint is empty")
		return nil, 0, "", fmt.Errorf("failed to create glusterfs REST client, REST URL is empty")
	}

	cli := gcli.NewClient(config.url, config.user, config.secretValue)
	if cli == nil {
		glog.Errorf("failed to create glusterfs REST client")
		return nil, 0, "", fmt.Errorf("failed to create glusterfs REST client, REST server authentication failed")
	}

	if config.clusterID != "" {
		clusterIDs = dstrings.Split(config.clusterID, ",")
		glog.V(4).Infof("provided clusterIDs %v", clusterIDs)
	}

	if config.volumeNamePrefix != "" {
		customVolumeName = fmt.Sprintf("%s_%s_%s_%s", config.volumeNamePrefix, p.options.PVC.Namespace, p.options.PVC.Name, uuid.NewUUID())
	}

	gid64 := int64(*gid)
	volumeReq := &gapi.VolumeCreateRequest{Size: sz, Name: customVolumeName, Clusters: clusterIDs, Gid: gid64, Durability: config.volumeType, GlusterVolumeOptions: p.volumeOptions}
	volume, err := cli.VolumeCreate(volumeReq)
	if err != nil {
		glog.Errorf("failed to create gluster volume: %v", err)
		return nil, 0, "", fmt.Errorf("failed to create gluster volume: %v", err)
	}

	glog.V(1).Infof("volume with size %d and name %s created", volume.Size, volume.Name)

	volID = volume.Id
	dynamicHostIps, err := getClusterNodes(cli, volume.Cluster)
	if err != nil {
		glog.Errorf("error [%v] when getting cluster nodes for volume %s", err, volume)
		return nil, 0, "", fmt.Errorf("error [%v] when getting cluster nodes for volume %s", err, volume)
	}

	epServiceName := dynamicEpSvcPrefix + p.options.PVC.Name
	epNamespace := p.options.PVC.Namespace
	endpoint, service, err := p.createEndpointService(epNamespace, epServiceName, dynamicHostIps, p.options.PVC.Name)
	if err != nil {
		glog.Errorf("failed to create endpoint/service %v/%v: %v", epNamespace, epServiceName, err)
		deleteErr := cli.VolumeDelete(volume.Id)
		if deleteErr != nil {
			glog.Errorf("error when deleting the volume: %v, manual deletion required", deleteErr)
		}
		return nil, 0, "", fmt.Errorf("failed to create endpoint/service %v/%v: %v", epNamespace, epServiceName, err)
	}
	glog.V(3).Infof("dynamic endpoint %v and service %v", endpoint, service)

	return &v1.GlusterfsVolumeSource{
		EndpointsName: endpoint.Name,
		Path:          volume.Name,
		ReadOnly:      false,
	}, sz, volID, nil
}

func (p *glusterfileProvisioner) createEndpointService(namespace string, epServiceName string, hostips []string, pvcname string) (endpoint *v1.Endpoints, service *v1.Service, err error) {

	addrlist := make([]v1.EndpointAddress, len(hostips))
	for i, v := range hostips {
		addrlist[i].IP = v
	}
	endpoint = &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      epServiceName,
			Labels: map[string]string{
				"gluster.org/provisioned-for-pvc": pvcname,
			},
		},
		Subsets: []v1.EndpointSubset{{
			Addresses: addrlist,
			Ports:     []v1.EndpointPort{{Port: 1, Protocol: "TCP"}},
		}},
	}
	kubeClient := p.client
	if kubeClient == nil {
		return nil, nil, fmt.Errorf("failed to get kube client when creating endpoint service")
	}
	_, err = kubeClient.CoreV1().Endpoints(namespace).Create(endpoint)
	if err != nil && errors.IsAlreadyExists(err) {
		glog.V(1).Infof("endpoint %s already exist in namespace %s", endpoint, namespace)
		err = nil
	}
	if err != nil {
		glog.Errorf("failed to create endpoint: %v", err)
		return nil, nil, fmt.Errorf("failed to create endpoint: %v", err)
	}

	service = &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      epServiceName,
			Namespace: namespace,
			Labels: map[string]string{
				"gluster.org/provisioned-for-pvc": pvcname,
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Protocol: "TCP", Port: 1}}}}
	_, err = kubeClient.CoreV1().Services(namespace).Create(service)
	if err != nil && errors.IsAlreadyExists(err) {
		glog.V(1).Infof("service %s already exist in namespace %s", service, namespace)
		err = nil
	}
	if err != nil {
		glog.Errorf("failed to create service: %v", err)
		return nil, nil, fmt.Errorf("error creating service: %v", err)
	}
	return endpoint, service, nil
}

func getClusterNodes(cli *gcli.Client, cluster string) (dynamicHostIps []string, err error) {
	clusterinfo, err := cli.ClusterInfo(cluster)
	if err != nil {
		glog.Errorf("failed to get cluster details: %v", err)
		return nil, fmt.Errorf("failed to get cluster details: %v", err)
	}

	// For the dynamically provisioned volume, we gather the list of node IPs
	// of the cluster on which provisioned volume belongs to, as there can be multiple
	// clusters.
	for _, node := range clusterinfo.Nodes {
		nodei, err := cli.NodeInfo(string(node))
		if err != nil {
			glog.Errorf("failed to get host ipaddress: %v", err)
			return nil, fmt.Errorf("failed to get host ipaddress: %v", err)
		}
		ipaddr := dstrings.Join(nodei.NodeAddRequest.Hostnames.Storage, "")
		dynamicHostIps = append(dynamicHostIps, ipaddr)
	}
	glog.V(3).Infof("host list :%v", dynamicHostIps)
	if len(dynamicHostIps) == 0 {
		glog.Errorf("no hosts found: %v", err)
		return nil, fmt.Errorf("no hosts found: %v", err)
	}
	return dynamicHostIps, nil
}

// getVolumeID returns volumeID from the PV or volumename.
func getVolumeID(pv *v1.PersistentVolume, volumeName string) (string, error) {
	volumeID := ""

	// Get volID from pvspec if available, else fill it from volumename.
	if pv != nil {
		if pv.Annotations[heketiVolIDAnn] != "" {
			volumeID = pv.Annotations[heketiVolIDAnn]
		} else {
			volumeID = dstrings.TrimPrefix(volumeName, volPrefix)
		}
	} else {
		return volumeID, fmt.Errorf("provided PV spec is nil")
	}
	if volumeID == "" {
		return volumeID, fmt.Errorf("volume ID is empty")
	}
	return volumeID, nil
}

func (p *glusterfileProvisioner) getRESTCredentials(pv *v1.PersistentVolume) (map[string]string, error) {
	restString, ok := pv.Annotations[restStr]
	if !ok {
		return nil, fmt.Errorf("volume annotation for server details not found on PV")
	}

	restStrSlice := dstrings.Split(restString, ",")
	heketiModeArgs := make(map[string]string)

	for _, v := range restStrSlice {
		if v != "" {
			s := dstrings.Split(v, ":")

			if s[0] == "url" {
				heketiModeArgs[s[0]] = dstrings.Join(s[1:], ":")
			} else {
				heketiModeArgs[s[0]] = s[1]
			}

		}
	}
	heketiModeArgs["restsecretvalue"] = ""
	if heketiModeArgs["secret"] != "" && heketiModeArgs["secretnamespace"] != "" {
		var err error
		heketiModeArgs["restsecretvalue"], err = parseSecret(heketiModeArgs["secretnamespace"], heketiModeArgs["secret"], p.client)
		if err != nil {
			glog.Errorf("failed to parse secret %s: %v", heketiModeArgs["secret"], err)
			return nil, err
		}
	}

	return heketiModeArgs, nil
}

func (p *glusterfileProvisioner) Delete(volume *v1.PersistentVolume) error {

	glog.V(1).Infof("deleting volume, path %s", volume.Spec.Glusterfs.Path)

	err := p.allocator.Release(volume)
	if err != nil {
		return err
	}

	volumeName := volume.Spec.Glusterfs.Path
	volumeID, err := getVolumeID(volume, volumeName)
	if err != nil {
		return fmt.Errorf("failed to get volumeID: %v", err)
	}

	heketiModeArgs, credErr := p.getRESTCredentials(volume)
	if credErr != nil {
		glog.Errorf("failed to retrieve REST credentials from pv: %v", credErr)
		return fmt.Errorf("failed to retrieve REST credentials from pv: %v", credErr)
	}

	cli := gcli.NewClient(heketiModeArgs["url"], heketiModeArgs["user"], heketiModeArgs["restsecretvalue"])
	if cli == nil {
		glog.Errorf("failed to create REST client")
		return fmt.Errorf("failed to create REST client, REST server authentication failed")
	}

	deleteErr := cli.VolumeDelete(volumeID)
	if deleteErr != nil {
		glog.Errorf("error when deleting the volume:%v", deleteErr)
		return deleteErr
	}
	glog.V(2).Infof("volume %s deleted successfully", volumeName)

	//Deleter takes endpoint and endpointnamespace from pv spec.
	pvSpec := volume.Spec
	var dynamicEndpoint, dynamicNamespace string
	if pvSpec.ClaimRef == nil {
		glog.Errorf("ClaimRef is nil")
		return fmt.Errorf("ClaimRef is nil")
	}
	if pvSpec.ClaimRef.Namespace == "" {
		glog.Errorf("namespace is nil")
		return fmt.Errorf("namespace is nil")
	}
	dynamicNamespace = pvSpec.ClaimRef.Namespace
	if pvSpec.Glusterfs.EndpointsName != "" {
		dynamicEndpoint = pvSpec.Glusterfs.EndpointsName
	}
	glog.V(3).Infof("dynamic namespace and endpoint : %v/%v", dynamicNamespace, dynamicEndpoint)
	err = p.deleteEndpointService(dynamicNamespace, dynamicEndpoint)
	if err != nil {
		glog.Errorf("error when deleting endpoint/service: %v", err)
	} else {
		glog.V(1).Infof("endpoint: %v/%v is deleted successfully", dynamicNamespace, dynamicEndpoint)
	}
	return nil

}
func (p *glusterfileProvisioner) deleteEndpointService(namespace string, epServiceName string) (err error) {
	kubeClient := p.client
	if kubeClient == nil {
		return fmt.Errorf("failed to get kube client when deleting endpoint service")
	}
	err = kubeClient.CoreV1().Services(namespace).Delete(epServiceName, nil)
	if err != nil {
		glog.Errorf("error deleting service %s/%s: %v", namespace, epServiceName, err)
		return fmt.Errorf("error deleting service %s/%s: %v", namespace, epServiceName, err)
	}
	glog.V(1).Infof("service/endpoint %s/%s deleted successfully", namespace, epServiceName)
	return nil
}

// parseSecret finds a given Secret instance and reads user password from it.
func parseSecret(namespace, secretName string, kubeClient kubernetes.Interface) (string, error) {

	secretMap, err := GetSecretForPV(namespace, secretName, provisionerName, kubeClient)
	if err != nil {
		glog.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
		return "", fmt.Errorf("failed to get secret %s/%s: %v", namespace, secretName, err)
	}
	if len(secretMap) == 0 {
		return "", fmt.Errorf("empty secret map")
	}
	secret := ""
	for k, v := range secretMap {
		if k == secretKeyName {
			return v, nil
		}
		secret = v
	}
	// If not found, the last secret in the map wins as done before
	return secret, nil
}

// GetSecretForPV locates secret by name and namespace, verifies the secret type, and returns secret map
func GetSecretForPV(restSecretNamespace, restSecretName, volumePluginName string, kubeClient kubernetes.Interface) (map[string]string, error) {
	secret := make(map[string]string)
	if kubeClient == nil {
		return secret, fmt.Errorf("Cannot get kube client")
	}
	secrets, err := kubeClient.Core().Secrets(restSecretNamespace).Get(restSecretName, metav1.GetOptions{})
	if err != nil {
		return secret, err
	}
	if secrets.Type != v1.SecretType(volumePluginName) {
		return secret, fmt.Errorf("cannot get secret of type %s", volumePluginName)
	}
	for name, data := range secrets.Data {
		secret[name] = string(data)
	}
	return secret, nil
}

func convertVolumeParam(volumeString string) (int, error) {

	count, err := strconv.Atoi(volumeString)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %q", volumeString)
	}

	if count < 0 {
		return 0, fmt.Errorf("negative values are not allowed")
	}
	return count, nil
}

// parseClassParameters parses StorageClass.Parameters
func (p *glusterfileProvisioner) parseClassParameters(params map[string]string, kubeclient kubernetes.Interface) (*provisionerConfig, error) {
	var cfg provisionerConfig
	var err error

	authEnabled := true
	parseVolumeType := ""
	parseVolumeOptions := ""
	parseVolumeNamePrefix := ""

	for k, v := range params {
		switch dstrings.ToLower(k) {
		case "resturl":
			cfg.url = v
		case "restuser":
			cfg.user = v
		case "restuserkey":
			cfg.userKey = v
		case "secretname":
			cfg.secretName = v
		case "secretnamespace":
			cfg.secretNamespace = v
		case "clusterid":
			if len(v) != 0 {
				cfg.clusterID = v
			}
		case "restauthenabled":
			authEnabled = dstrings.ToLower(v) == "true"

		case "volumetype":
			parseVolumeType = v

		case "volumeoptions":
			if len(v) != 0 {
				parseVolumeOptions = v
			}
		case "volumenameprefix":
			if len(v) != 0 {
				parseVolumeNamePrefix = v
			}
		case "gidmin":
		case "gidmax":
		default:
			return nil, fmt.Errorf("invalid option %q for volume plugin %s", k, provisionerName)
		}
	}

	if len(cfg.url) == 0 {
		return nil, fmt.Errorf("StorageClass for provisioner %s must contain 'resturl' parameter", provisionerName)
	}

	if len(parseVolumeType) == 0 {
		cfg.volumeType = gapi.VolumeDurabilityInfo{Type: gapi.DurabilityReplicate, Replicate: gapi.ReplicaDurability{Replica: replicaCount}}
	} else {
		parseVolumeTypeInfo := dstrings.Split(parseVolumeType, ":")

		switch parseVolumeTypeInfo[0] {
		case "replicate":
			if len(parseVolumeTypeInfo) >= 2 {
				newReplicaCount, convertErr := convertVolumeParam(parseVolumeTypeInfo[1])
				if convertErr != nil {
					return nil, fmt.Errorf("error %v when parsing value %q of option %s for volume plugin %s", convertErr, parseVolumeTypeInfo[1], "volumetype", provisionerName)
				}
				cfg.volumeType = gapi.VolumeDurabilityInfo{Type: gapi.DurabilityReplicate, Replicate: gapi.ReplicaDurability{Replica: newReplicaCount}}
			} else {
				cfg.volumeType = gapi.VolumeDurabilityInfo{Type: gapi.DurabilityReplicate, Replicate: gapi.ReplicaDurability{Replica: replicaCount}}
			}
		case "disperse":
			if len(parseVolumeTypeInfo) >= 3 {
				newDisperseData, convertErr := convertVolumeParam(parseVolumeTypeInfo[1])
				if err != nil {
					return nil, fmt.Errorf("error %v when parsing value %q of option %s for volume plugin %s", parseVolumeTypeInfo[1], convertErr, "volumetype", provisionerName)
				}
				newDisperseRedundancy, convertErr := convertVolumeParam(parseVolumeTypeInfo[2])
				if err != nil {
					return nil, fmt.Errorf("error %v when parsing value %q of option %s for volume plugin %s", convertErr, parseVolumeTypeInfo[2], "volumetype", provisionerName)
				}
				cfg.volumeType = gapi.VolumeDurabilityInfo{Type: gapi.DurabilityEC, Disperse: gapi.DisperseDurability{Data: newDisperseData, Redundancy: newDisperseRedundancy}}
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %q must have data:redundancy count set for disperse volumes in storage class option '%s'", provisionerName, "volumetype")
			}
		case "none":
			cfg.volumeType = gapi.VolumeDurabilityInfo{Type: gapi.DurabilityDistributeOnly}
		default:
			return nil, fmt.Errorf("error parsing value for option 'volumetype' for volume plugin %s", provisionerName)
		}
	}
	if !authEnabled {
		cfg.user = ""
		cfg.secretName = ""
		cfg.secretNamespace = ""
		cfg.userKey = ""
		cfg.secretValue = ""
	}

	if len(cfg.secretName) != 0 || len(cfg.secretNamespace) != 0 {
		// secretName + Namespace has precedence over userKey
		if len(cfg.secretName) != 0 && len(cfg.secretNamespace) != 0 {

			cfg.secretValue, err = parseSecret(cfg.secretNamespace, cfg.secretName, p.client)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("StorageClass for provisioner %q must have secretNamespace and secretName either both set or both empty", provisionerName)
		}
	} else {
		cfg.secretValue = cfg.userKey
	}

	if len(parseVolumeOptions) != 0 {
		volOptions := dstrings.Split(parseVolumeOptions, ",")
		if len(volOptions) == 0 {
			return nil, fmt.Errorf("StorageClass for provisioner %q must have valid (for e.g.,'client.ssl on') volume option", provisionerName)
		}
		cfg.volumeOptions = volOptions

	}

	if len(parseVolumeNamePrefix) != 0 {
		if dstrings.Contains(parseVolumeNamePrefix, "_") {
			return nil, fmt.Errorf("Storageclass parameter 'volumenameprefix' should not contain '_' in its value")
		}
		cfg.volumeNamePrefix = parseVolumeNamePrefix
	}
	return &cfg, nil
}

var (
	master     = flag.String("master", "", "Master URL")
	kubeconfig = flag.String("kubeconfig", "", "Absolute path to the kubeconfig")
	id         = flag.String("id", "", "Unique provisioner identity")
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes

	var config *rest.Config
	var err error
	if *master != "" || *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags(*master, *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		glog.Fatalf("Failed to create config: %v", err)
	}

	prName := provisionerName
	provName := os.Getenv(provisionerNameKey)

	// Precedence is given for ProvisionerNameKey
	if provName != "" && *id != "" {
		prName = provName
	}

	if provName == "" && *id != "" {
		prName = *id
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client:%v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version:%v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	glusterfileProvisioner := NewglusterfileProvisioner(clientset, prName)

	// Start the provision controller which will dynamically provision glusterfs
	// PVs

	pc := controller.NewProvisionController(
		clientset,
		prName,
		glusterfileProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
