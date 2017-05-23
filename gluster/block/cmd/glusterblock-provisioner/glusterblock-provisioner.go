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
	"os"
	exec "os/exec"
	"strconv"
	dstrings "strings"

	"github.com/golang/glog"
	gcli "github.com/heketi/heketi/client/api/go-client"
	gapi "github.com/heketi/heketi/pkg/glusterfs/api"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	provisionerName    = "gluster.org/glusterblock"
	secretKeyName      = "key"
	provisionerNameKey = "PROVISIONER_NAME"
	shareIDAnn         = "glusterBlockShare"
	provisionerIDAnn   = "glusterBlkProvIdentity"
	creatorAnn         = "kubernetes.io/createdby"
	volumeTypeAnn      = "gluster.org/type"
	descAnn            = "Gluster: Dynamically provisioned PV"
	provisionerVersion = "v0.9"
	chapType           = "kubernetes.io/iscsi-chap"
	heketiAnn          = "heketi-dynamic-provisioner"
	volPrefix          = "blockvol-"
	defaultIqn         = "iqn.2016-12.org.gluster-block:aafea465-9167-4880-b37c-2c36db8562ea"
	defaultPortal      = "192.168.1.11"
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

	// Optional:  Heketi clusterID from which the provisioner create the block volume
	clusterID string

	// Optional: high availability count for multipathing
	haCount int

	// Optional: Operation mode  (heketi, gluster-block)
	opMode string

	// Optional: Gluster Block command Args.
	blockModeArgs map[string]string

	// Optional: Heketi Service parameters.
	heketiModeArgs map[string]string

	// Optional: Chap Auth Enable
	chapAuthEnabled bool
}

type glusterBlockVolume struct {
	TargetPortal      string
	Portals           []string `json:"PORTAL(S)"`
	Iqn               string   `json:"IQN"`
	Name              string   `json:"NAME"`
	User              string   `json:"USERNAME"`
	AuthKey           string   `json:"PASSWORD"`
	Paths             int      `json:"HA"`
	Lun               int32
	FSType            string
	ISCSIInterface    string
	DiscoveryCHAPAuth bool
	SessionCHAPAuth   bool
	ReadOnly          bool
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
		return nil, fmt.Errorf("glusterblock: claim Selector is not supported")
	}

	glog.V(4).Infof("glusterblock: VolumeOptions %v", options)

	cfg, err := parseClassParameters(options.Parameters, p.client)
	if err != nil {
		return nil, fmt.Errorf("glusterblock: failed to parse class parameters: %v", err)
	}
	p.provConfig = *cfg

	glog.V(4).Infof("glusterblock: creating volume with configuration %+v", p.provConfig)

	blockVol := volPrefix + string(uuid.NewUUID())
	vol, err := p.createVolume(blockVol)
	if err != nil {
		return nil, fmt.Errorf("glusterblock: failed to create volume: %v", err)
	}

	if len(vol.Portals) == 1 && vol.Portals[0] != "" {
		vol.TargetPortal = vol.Portals[0]
		vol.Portals = nil
	} else {

		portals := vol.Portals
		vol.Portals = nil
		for _, v := range portals {
			if v != "" && vol.TargetPortal == "" {
				vol.TargetPortal = v
				continue
			} else {
				vol.Portals = append(vol.Portals, v)
			}
		}
	}

	//TODO: move to info
	glog.V(1).Infof("glusterblock: Volume configuration : %+v", vol)

	nameSpace := options.PVC.Namespace
	user := vol.User
	password := vol.AuthKey
	secretName := "glusterblk-" + user + "-secret"
	secretRef := &v1.LocalObjectReference{}

	if p.provConfig.chapAuthEnabled && user != "" && password != "" {
		secretRef, err = p.createSecret(nameSpace, secretName, user, password)
		if err != nil {
			glog.Errorf("glusterblock: failed to create credentials for pv")
			return nil, fmt.Errorf("glusterblock: failed to create credentials for pv")
		}
		vol.SessionCHAPAuth = p.provConfig.chapAuthEnabled
	} else {
		glog.V(1).Infof("glusterblock: authentication is nil")
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn:   p.identity,
				provisionerVersion: provisionerVersion,
				shareIDAnn:         blockVol,
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
					Portals:         vol.Portals,
					IQN:             vol.Iqn,
					Lun:             0,
					FSType:          "ext4",
					ReadOnly:        false,
					SessionCHAPAuth: vol.SessionCHAPAuth,
					SecretRef:       secretRef,
				},
			},
		},
	}
	glog.Infof("successfully created Gluster Block volume %+v", pv.Spec.PersistentVolumeSource.ISCSI)
	return pv, nil
}

func (p *glusterBlockProvisioner) createSecret(nameSpace string, secretName string, user string, password string) (*v1.LocalObjectReference, error) {
	var err error

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
	secretRef := &v1.LocalObjectReference{}
	if secretRef != nil {
		secretRef.Name = secretName
		glog.V(1).Infof("glusterblock: secret [%v]: secretRef [%v]", secret, secretRef)

	}
	return secretRef, nil
}

// createVolume creates a gluster block volume i.e. the storage asset.
func (p *glusterBlockProvisioner) createVolume(blockVol string) (*glusterBlockVolume, error) {

	volSize := p.options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := volSize.Value()
	volszInt := int(util.RoundUpSize(volSizeBytes, 1024*1024*1024))
	sizeStr := strconv.Itoa(volszInt)
	glog.V(2).Infof("glusterfs: create block volume of size: %d bytes and configuration %+v", volSizeBytes, p.provConfig)

	// Possible opModes are gluster-block and heketi:
	switch p.provConfig.opMode {

	case "gluster-block":
		haCountStr := "1"
		cmd := exec.Command(
			p.provConfig.opMode, "create", p.provConfig.blockModeArgs["glustervol"]+"/"+blockVol,
			"ha", haCountStr, p.provConfig.blockModeArgs["hosts"], sizeStr, "--json")

		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			glog.Errorf("glusterblock: command [%v] failed: %v", cmd, cmdErr)
			return nil, fmt.Errorf("gluster block command failed")
		}
		// Fill the block configuration.
		blockRes := &p.volConfig
		json.Unmarshal([]byte(out), blockRes)

		//TODO:
		dTarget := blockRes.Portals[0]

		if p.provConfig.chapAuthEnabled {
			cmd := exec.Command(
				p.provConfig.opMode, "modify", p.provConfig.blockModeArgs["glustervol"]+"/"+blockVol,
				"auth", "enable", "--json")

			out, cmdErr := cmd.CombinedOutput()
			if cmdErr != nil {
				glog.Errorf("glusterblock: error [%v] when running command %v", cmdErr, cmd)
				return nil, cmdErr
			}
			json.Unmarshal([]byte(out), blockRes)

			if blockRes.User == "" || blockRes.AuthKey == "" {
				return nil, fmt.Errorf("gluster-block: missing CHAP - invalid volume creation ")
			}

		}
		if dTarget == "" || blockRes.Iqn == "" {
			return nil, fmt.Errorf("gluster-block: missing IQN - invalid volume creation")
		}

	case "heketi":
		cli := gcli.NewClient(p.provConfig.url, p.provConfig.user, p.provConfig.secretValue)
		if cli == nil {
			glog.Errorf("glusterfs: failed to create glusterfs rest client")
			return nil, fmt.Errorf("glusterfs: failed to create glusterfs rest client, REST server authentication failed")
		}
		// TODO: call blockvolcreate
		volumeReq := &gapi.VolumeCreateRequest{Size: volszInt}
		_, err := cli.VolumeCreate(volumeReq)
		if err != nil {
			glog.Errorf("glusterfs: error creating volume %v ", err)
			return nil, fmt.Errorf("error creating volume %v", err)
		}

		// TODO: Fill the params
		p.volConfig.Iqn = defaultIqn
		p.volConfig.TargetPortal = defaultPortal

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
	chapAuthEnabled := true
	parseOpmode := ""
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
		case "clusterids":
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
			cfg.haCount = haCount
		case "opmode":
			parseOpmode = v
		case "chapauth":
			chapAuthEnabled = dstrings.ToLower(v) == "true"
			cfg.chapAuthEnabled = chapAuthEnabled
		default:
			return nil, fmt.Errorf("glusterblock: invalid option %q for volume plugin %s", k, "glusterblock")
		}
	}

	//TODO:
	if len(cfg.url) == 0 {
		return nil, fmt.Errorf("StorageClass for provisioner %s must contain 'resturl' parameter", "glusterblock")
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
		case "hosts":
			blockHosts := dstrings.Split(v, "=")[1]
			if blockHosts != "" {
				modeArgs["hosts"] = blockHosts
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %s must contain valid  parameter for %v", "glusterblock", "hosts")
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
	glusterBlockProvisioner := NewGlusterBlockProvisioner(clientset, prName)

	// Start the provision controller which will dynamically provision glusterblock
	// PVs

	pc := controller.NewProvisionController(
		clientset,
		prName,
		glusterBlockProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
