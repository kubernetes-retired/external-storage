/*
Copyright 2017 The Kubernetes Authors.

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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	exec "os/exec"
	"strconv"
	dstrings "strings"

	"github.com/golang/glog"
	gcli "github.com/heketi/heketi/client/api/go-client"
	gapi "github.com/heketi/heketi/pkg/glusterfs/api"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	//k8s.io/kubernetes/pkg/volume
	//storage "k8s.io/client-go/pkg/apis/storage/v1beta1"
)

const (
	provisionerName    = "gluster.org/glusterblock"
	secretKeyName      = "key"
	shareIDAnn         = "glusterBlockShare"
	provisionerIDAnn   = "glusterBlockProvisionerIdentity"
	creatorAnn         = "kubernetes.io/createdby"
	volumeTypeAnn      = "gluster.org/type"
	descAnn            = "Gluster: Dynamically provisioned PV"
	provisionerVersion = "v0.6"
	chapType           = "kubernetes.io/iscsi-chap"
	heketiAnn          = "heketi-dynamic-provisioner"
	volPrefix          = "blockvol-"
)

type glusterBlockProvisioner struct {
	// Kubernetes Client. Use to retrieve Gluster admin secret
	client kubernetes.Interface

	// Identity of this glusterBlockProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string

	// Configuration of gluster block provisioner
	provConfig provisionerConfig

	// Configuration of block volume
	volConfig glusterBlockVolume

	options controller.VolumeOptions
}

type provisionerConfig struct {
	// Required:  this is the Rest Service Url ( ex: Heketi) for Gluster Block
	url string

	// Optional: Rest user who is capable of creating gluster block volumes.
	user string

	// Optional: Rest user key for above RestUser.
	userKey string

	// Optional:  secret name, namespace.
	secretNamespace string
	secretName      string
	secretValue     string

	// Optional:  clusterID from which the provisioner create the block volume
	clusterID string

	// Optional: high availability count in case of multipathing
	haCount int

	// Optional: Operation mode  (heketi, gluster-block)
	opMode string

	// Optional: Gluster Block command Args or Heketi command args
	blockModeArgs map[string]string
}

type glusterBlockVolume struct {
	TargetPortal      string
	Portals           []string `json:"PORTAL(S)"`
	Iqn               string   `json:"IQN"`
	Lun               int32
	FSType            string
	ISCSIInterface    string
	DiscoveryCHAPAuth bool
	SessionCHAPAuth   bool
	ReadOnly          bool
	Name              string `json:"NAME"`
	GbID              string `json:"GBID"`
	Paths             int    `json:"HA"`
}

//NewGlusterBlockProvisioner create a new provisioner.
func NewGlusterBlockProvisioner(client kubernetes.Interface, id string) controller.Provisioner {
	return &glusterBlockProvisioner{
		client:   client,
		identity: id,
	}
}

var _ controller.Provisioner = &glusterBlockProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *glusterBlockProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {

	var err error
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	glog.V(4).Infof("glusterblock: VolumeOptions %v", options)

	cfg, err := parseClassParameters(options.Parameters, p.client)
	if err != nil {
		return nil, err
	}
	p.provConfig = *cfg

	glog.V(4).Infof("glusterfs: creating volume with configuration %+v", p.provConfig)

	// Create unique PVC identity.
	blockVolIdentity := fmt.Sprintf("kubernetes-dynamic-pvc-%s", uuid.NewUUID())

	//TODO: blockVol := volPrefix + string(uuid.NewUUID())
	vol, err := p.createVolume(blockVolIdentity)
	if err != nil {
		return nil, err
	}

	// TODO:
	vol.TargetPortal = vol.Portals[0]
	nameSpace := options.PVC.Namespace

	// TODO: fetch user from info response.
	user := fmt.Sprintf("user-%s", uuid.NewUUID())
	password := "4a5c9b84-3a6d-44b4-9668-c9a6d699a5e9"
	secretName := "glusterblk-" + user + "-secret"
	secretRef := &v1.LocalObjectReference{}
	if user != "" && password != "" {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: nameSpace,
				Name:      secretName,
			},
			Data: map[string][]byte{
				"node.session.auth.username": []byte(user),
				"node.session.auth.password": []byte(password),
			},
			Type: chapType,
		}
		_, err = p.client.Core().Secrets(nameSpace).Create(secret)
		if err != nil {
			return nil, fmt.Errorf("glusterblock: failed to create secret, error %v", err)
		}

		if secretRef != nil {
			secretRef.Name = secretName
			glog.V(1).Infof("gluster block secret [%v]: secretRef [%v]", secret, secretRef)
		}
	} else {
		glog.V(1).Infof("gluster block response does not contain username and password")
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn:   p.identity,
				provisionerVersion: provisionerVersion,
				shareIDAnn:         blockVolIdentity,
				creatorAnn:         heketiAnn,
				volumeTypeAnn:      "block",
				"Description":      descAnn,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				ISCSI: &v1.ISCSIVolumeSource{
					TargetPortal:    vol.TargetPortal,
					IQN:             vol.Iqn,
					Lun:             0,
					FSType:          "ext4",
					ReadOnly:        false,
					SessionCHAPAuth: true,
					SecretRef:       secretRef,
				},
			},
		},
	}
	glog.Infof("successfully created Gluster Block volume %+v", pv.Spec.PersistentVolumeSource.ISCSI)
	return pv, nil
}

// createVolume creates a gluster block volume i.e. the storage asset.
func (p *glusterBlockProvisioner) createVolume(blockVol string) (*glusterBlockVolume, error) {
	/*
		TODO: calculation of size
		volSize := p.options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
		volSizeBytes := volSize.Value()
		sz := int(volume.RoundUpSize(volSizeBytes, 1024*1024*1024))
	*/

	volSizeBytes := "1073741824"
	glog.V(2).Infof("glusterfs: create block volume of size: %d bytes and configuration %+v", volSizeBytes, p.provConfig)

	// Possible opModes are gluster-block and heketi:
	switch p.provConfig.opMode {

	case "gluster-block":
		haCountStr := "1"
		cmd := exec.Command(
			p.provConfig.opMode, "create",
			p.provConfig.blockModeArgs["glustervol"]+"/"+blockVol,
			"ha", haCountStr,
			p.provConfig.blockModeArgs["confighosts"],
			"2GiB", "--json")

		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			glog.Errorf("glusterblock: error [%v] when running command %v", cmdErr, cmd)
			return nil, cmdErr
		}

		blockRes := &p.volConfig
		json.Unmarshal([]byte(out), blockRes)
		dTarget := blockRes.Portals[0]
		dIqn := blockRes.Iqn
		if dTarget == "" || dIqn == "" {
			return nil, fmt.Errorf("gluster-block: invalid volume creation")
		}

	case "heketi":
		cli := gcli.NewClient(p.provConfig.url, p.provConfig.user, p.provConfig.secretValue)
		if cli == nil {
			glog.Errorf("glusterfs: failed to create glusterfs rest client")
			return nil, fmt.Errorf("glusterfs: failed to create glusterfs rest client, REST server authentication failed")
		}

		//TODO:
		sz, _ := strconv.Atoi(volSizeBytes)
		volumeReq := &gapi.VolumeCreateRequest{Size: sz}
		_, err := cli.VolumeCreate(volumeReq)
		if err != nil {
			glog.Errorf("glusterfs: error creating volume %v ", err)
			return nil, fmt.Errorf("error creating volume %v", err)
		}
		p.volConfig.Iqn = "iqn.2016-12.org.gluster-block:aafea465-9167-4880-b37c-2c36db8562ea"
		p.volConfig.TargetPortal = "192.168.1.11"

	default:
		return nil, fmt.Errorf("error parsing value for 'opmode' for volume plugin %s", provisionerName)
	}
	return &p.volConfig, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *glusterBlockProvisioner) Delete(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[provisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{Reason: "identity annotation on PV does not match this provisioners identity"}
	}

	delBlockVol, ok := volume.Annotations[shareIDAnn]
	if !ok {
		return errors.New("gluster block share annotation not found on PV")
	}

	// Delete this blockVol
	glog.V(1).Infof("blockVolume  %v", delBlockVol)

	switch p.provConfig.opMode {
	case "gluster-block":
		glog.V(1).Infof("gluster-block: Deleteing Volume %v ", delBlockVol)
		deleteCmd := exec.Command(
			p.provConfig.opMode, "delete",
			p.provConfig.blockModeArgs["glustervol"]+"/"+delBlockVol, "--json")
		_, cmdErr := deleteCmd.CombinedOutput()
		if cmdErr != nil {
			glog.Errorf("glusterblock: error [%v] when running command %v", cmdErr, deleteCmd)
			return cmdErr
		}
		glog.V(1).Infof("gluster-block: Successfully deleted Volume %v ", delBlockVol)

	case "heketi":
		glog.V(1).Infof("heketik: Deleteing Volume %v", delBlockVol)
	default:
		glog.Errorf("Unknown OpMode, failed to delete volume %v", delBlockVol)

	}

	return nil
}

func parseClassParameters(params map[string]string, kubeclient kubernetes.Interface) (*provisionerConfig, error) {
	var cfg provisionerConfig
	var err error

	authEnabled := true
	parseOpmode := ""
	// Default multipath count has been set to 3
	haCount := 3

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
		//TODO: Use clusterid if required.
		case "clusterID":
			if len(v) != 0 {
				cfg.clusterID = v
			}
		case "restauthenabled":
			authEnabled = dstrings.ToLower(v) == "true"
		case "hacount":
			haCount, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("glusterblock: failed to parse hacount %v ", k)
			}
		case "opmode":
			parseOpmode = v
		default:
			return nil, fmt.Errorf("glusterblock: invalid option %q for volume plugin %s", k, "glusterblock")
		}
	}

	//TODO:
	if len(cfg.url) == 0 {
		return nil, fmt.Errorf("StorageClass for provisioner %s must contain 'resturl' parameter", "glusterblock")
	}

	if haCount == 0 {
		cfg.haCount = haCount
	}

	if len(parseOpmode) == 0 {
		cfg.opMode = "gluster-block"
	} else {
		parseOpmodeInfo := dstrings.Split(parseOpmode, ":")

		switch parseOpmodeInfo[0] {
		// Gluster Block opmode
		case "gluster-block":
			if len(parseOpmodeInfo) >= 2 {
				cfg.opMode = "gluster-block"
				argsDict, err := parseBlockModeArgs(cfg.opMode, parseOpmodeInfo[1])
				if err != nil {
					glog.Errorf("Failed to parse gluster-block arguments")
					return nil, fmt.Errorf("Failed to parse gluster-block arguments")
				}
				cfg.blockModeArgs = *argsDict
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %s contains wrong number of arguments for %s", "glusterblock", parseOpmode)
			}

			// Heketi Opmode
		case "heketi":
			if len(parseOpmodeInfo) >= 2 {
				cfg.opMode = "heketi"
				// TODO:
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %s contains wrong number of arguments for %s", "heketi", parseOpmode)
			}

		default:
			return nil, fmt.Errorf("StorageClass for provisioner %s contains unknown [%v] parameter", "glusterblock", parseOpmodeInfo[0])
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
			cfg.secretValue, err = parseSecret(cfg.secretNamespace, cfg.secretName, kubeclient)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("StorageClass for provisioner %q must have secretNamespace and secretName either both set or both empty", "glusterblock")
		}
	} else {
		cfg.secretValue = cfg.userKey
	}

	return &cfg, nil
}

func parseBlockModeArgs(mode string, inArgs string) (*map[string]string, error) {
	modeArgs := make(map[string]string)
	modeCommandParams := dstrings.Split(inArgs, ",")
	for _, v := range modeCommandParams {
		paramInfo := dstrings.Split(v, "=")
		switch paramInfo[0] {
		case "glustervol":
			volName := dstrings.Split(v, "=")[1]
			if volName != "" {
				modeArgs["glustervol"] = volName
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %s must contain valid parameter for %v ", "glusterblock", "glustervol")
			}
		case "confighosts":
			blockHosts := dstrings.Split(v, "=")[1]
			if blockHosts != "" {
				modeArgs["confighosts"] = blockHosts
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %s must contain valid  parameter for %v", "glusterblock", "confighosts")
			}
		default:
			return nil, fmt.Errorf("parseBlockModeArgs: StorageClass for provisioner %s must contain valid parameter for %v", "glusterblock", mode)
		}
	}
	return &modeArgs, nil
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
func GetSecretForPV(secretNamespace, secretName, volumePluginName string, kubeClient kubernetes.Interface) (map[string]string, error) {
	secret := make(map[string]string)
	if kubeClient == nil {
		return secret, fmt.Errorf("Cannot get kube client")
	}
	secrets, err := kubeClient.Core().Secrets(secretNamespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return secret, err
	}
	if secrets.Type != v1.SecretType(volumePluginName) {
		return secret, fmt.Errorf("Cannot get secret of type %s", volumePluginName)
	}
	for name, data := range secrets.Data {
		secret[name] = string(data)
	}
	return secret, nil
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

	prID := string(uuid.NewUUID())

	if *id != "" {
		prID = *id
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	glusterBlockProvisioner := NewGlusterBlockProvisioner(clientset, prID)

	// Start the provision controller which will dynamically provision glusterblock
	// PVs

	pc := controller.NewProvisionController(
		clientset,
		provisionerName,
		glusterBlockProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
