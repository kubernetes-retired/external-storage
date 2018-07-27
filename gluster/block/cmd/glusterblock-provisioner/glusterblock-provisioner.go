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
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
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
	descAnn            = "Gluster-external: Dynamically provisioned PV"
	provisionerVersion = "v2.0.0"
	chapType           = "kubernetes.io/iscsi-chap"
	blockVolPrefix     = "blockvol_"
	heketiOpmode       = "heketi"
	glusterBlockOpmode = "gluster-block"
	volIDAnn           = "gluster.org/volume-id"
	errIDNotFound      = "ID not found"
)

type glusterBlockProvisioner struct {
	// Kubernetes Client. Use to retrieve Gluster admin secret
	client kubernetes.Interface

	// Identity of this glusterBlockProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string

	options controller.VolumeOptions
}

type provisionerConfig struct {
	// Required:  this is the Rest Service Url ( ex: Heketi) for Gluster Block
	url string

	// Optional: Rest user who is capable of creating gluster block volumes.
	user string

	// Optional: Rest Auth Enable.
	restAuthEnabled bool

	// Optional:  secret name, namespace.
	restSecretNamespace string
	restSecretName      string
	restSecretValue     string

	// Optional:  Heketi clusterID from which the provisioner create the block volume
	clusterID string

	// Optional: high availability count for multipathing
	haCount int

	// Optional: Operation mode  (heketi, gluster-block)
	opMode string

	// Optional: Gluster Block command Args.
	blockModeArgs map[string]string

	// Optional: Chap Auth Enable
	chapAuthEnabled bool

	// Optional: Custom Volume name prefix string.
	volumeNamePrefix string
}

type glusterBlockVolume struct {
	*glusterBlockExecVolRes
	*heketiBlockVolRes
	*iscsiSpec
}

type glusterBlockExecVolRes struct {
	Portals []string `json:"PORTAL(S)"`
	Iqn     string   `json:"IQN"`
	Name    string   `json:"NAME"`
	User    string   `json:"USERNAME"`
	AuthKey string   `json:"PASSWORD"`
	Paths   int      `json:"HA"`
}

type heketiBlockVolRes struct {
	ID      string   `json:"id"`
	Portals []string `json:"hosts"`
	Iqn     string   `json:"iqn"`
	Lun     int      `json:"lun"`
	User    string   `json:"username"`
	AuthKey string   `json:"password"`
	Cluster string   `json:"cluster,omitempty"`
}

type iscsiSpec struct {
	TargetPortal      string
	Portals           []string
	User              string
	AuthKey           string
	Iqn               string
	Lun               int
	FSType            string
	ISCSIInterface    string
	DiscoveryCHAPAuth bool
	SessionCHAPAuth   bool
	ReadOnly          bool
	BlockSecret       string
	BlockSecretNs     string
	BlockVolName      string
	VolumeID          string
}

//NewGlusterBlockProvisioner create a new provisioner.
func NewGlusterBlockProvisioner(client kubernetes.Interface, id string) controller.Provisioner {
	return &glusterBlockProvisioner{
		client:   client,
		identity: id,
	}
}

var _ controller.Provisioner = &glusterBlockProvisioner{}

func (p *glusterBlockProvisioner) GetAccessModes() []v1.PersistentVolumeAccessMode {
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
		v1.ReadOnlyMany,
	}
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *glusterBlockProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {

	var err error
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}

	if !util.AccessModesContainedInAll(p.GetAccessModes(), options.PVC.Spec.AccessModes) {
		return nil, fmt.Errorf("invalid AccessModes %v: only AccessModes %v are supported", options.PVC.Spec.AccessModes, p.GetAccessModes())
	}

	glog.V(4).Infof("VolumeOptions %v", options)

	cfg, parseErr := parseClassParameters(options.Parameters, p.client)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse storage class parameters: %v", parseErr)
	}

	glog.V(4).Infof("creating volume with configuration %+v", *cfg)

	// Calculate the size
	volSize := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := volSize.Value()
	volszInt := int(util.RoundUpToGiB(volSizeBytes))

	// Create gluster block Volume
	blockVolName := ""
	if cfg.opMode == glusterBlockOpmode {
		blockVolName = blockVolPrefix + string(uuid.NewUUID())
	}

	if cfg.volumeNamePrefix != "" {
		blockVolName = fmt.Sprintf("%s_%s_%s_%s", cfg.volumeNamePrefix, options.PVC.Namespace, options.PVC.Name, uuid.NewUUID())
	}

	blockVol, createErr := p.createVolume(volszInt, blockVolName, cfg)
	if createErr != nil {
		return nil, fmt.Errorf("failed to create volume: %v", createErr)
	}

	iscsiVol := &iscsiSpec{}
	if blockVol != nil {
		blockVol.iscsiSpec = iscsiVol
	}

	storeErr := p.storeFieldsFromResponse(blockVolName, cfg, blockVol, iscsiVol)
	if storeErr != nil {
		return nil, fmt.Errorf("failed to store response fields to iscsi volume spec: %v", storeErr)
	}
	sortErr := p.sortTargetPortal(iscsiVol)
	if sortErr != nil {
		return nil, fmt.Errorf("failed to fetch Target Portal %v from iscsi volume spec", sortErr)
	}

	if iscsiVol.TargetPortal == "" || iscsiVol.Iqn == "" {
		return nil, fmt.Errorf("failed to create volume, Target portal/IQN is nil in iscsi volume spec")
	}

	glog.V(1).Infof("Volume configuration %+v", blockVol)

	secretRef := &v1.SecretReference{}

	if cfg.chapAuthEnabled && iscsiVol.User != "" && iscsiVol.AuthKey != "" {
		nameSpace := options.PVC.Namespace
		secretName := "glusterblk-" + iscsiVol.User + "-secret"
		secretRef, err = p.createSecretRef(nameSpace, secretName, iscsiVol.User, iscsiVol.AuthKey)
		if err != nil {
			glog.Errorf("failed to create CHAP auth credentials for pv: %v", err)
			return nil, fmt.Errorf("failed to create CHAP auth credentials for pv: %v", err)

		}
		iscsiVol.SessionCHAPAuth = cfg.chapAuthEnabled
		iscsiVol.BlockSecret = secretName
		iscsiVol.BlockSecretNs = nameSpace
	} else if !(cfg.chapAuthEnabled) {
		glog.V(1).Infof("CHAP authentication is not requested for this PV")
		iscsiVol.SessionCHAPAuth = false
		secretRef = nil
	} else {
		glog.Errorf("chapauth enabled - but CHAP credentials are missing in the %v response", cfg.opMode)
		return nil, fmt.Errorf("chapauth enabled - but CHAP credentials are missing in the %v response", cfg.opMode)
	}

	var blockString []string
	modeAnn := ""
	if cfg.opMode == glusterBlockOpmode {
		for k, v := range cfg.blockModeArgs {
			blockString = append(blockString, k+":"+v)
			modeAnn = dstrings.Join(blockString, ",")
		}
	} else {
		blockString = nil
		modeAnn = "url:" + cfg.url + "," + "user:" + cfg.user + "," + "secret:" + cfg.restSecretName + "," + "secretnamespace:" + cfg.restSecretNamespace
	}

	volMode := options.PVC.Spec.VolumeMode
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn:   p.identity,
				provisionerVersion: provisionerVersion,
				shareIDAnn:         iscsiVol.BlockVolName,
				creatorAnn:         cfg.opMode,
				volumeTypeAnn:      "block",
				"Description":      descAnn,
				"Blockstring":      modeAnn,
				"AccessKey":        iscsiVol.BlockSecret,
				"AccessKeyNs":      iscsiVol.BlockSecretNs,
				volIDAnn:           iscsiVol.VolumeID,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			VolumeMode:                    volMode,
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				ISCSI: &v1.ISCSIPersistentVolumeSource{
					TargetPortal:    iscsiVol.TargetPortal,
					Portals:         iscsiVol.Portals,
					IQN:             iscsiVol.Iqn,
					Lun:             0,
					FSType:          "xfs",
					ReadOnly:        false,
					SessionCHAPAuth: iscsiVol.SessionCHAPAuth,
					SecretRef:       secretRef,
				},
			},
		},
	}
	glog.V(1).Infof("successfully created Gluster Block volume %+v", pv.Spec.PersistentVolumeSource.ISCSI)
	return pv, nil
}

func (p *glusterBlockProvisioner) storeFieldsFromResponse(blockVolName string, cfg *provisionerConfig, blockVol *glusterBlockVolume, iscsiVol *iscsiSpec) error {
	//Store fields from response to iscsiSpec struct

	if cfg == nil || blockVol == nil {
		return fmt.Errorf("provisioner config %v or blockvol %v is nil", cfg, blockVol)
	}

	if cfg.opMode == heketiOpmode && blockVol.heketiBlockVolRes != nil {
		iscsiVol.Portals = blockVol.heketiBlockVolRes.Portals
		iscsiVol.Iqn = blockVol.heketiBlockVolRes.Iqn
		iscsiVol.User = blockVol.heketiBlockVolRes.User
		iscsiVol.AuthKey = blockVol.heketiBlockVolRes.AuthKey
		if cfg.volumeNamePrefix != "" {
			iscsiVol.BlockVolName = blockVolName
		} else {
			iscsiVol.BlockVolName = blockVolPrefix + blockVol.heketiBlockVolRes.ID
		}
		iscsiVol.VolumeID = blockVol.heketiBlockVolRes.ID
	} else if cfg.opMode == glusterBlockOpmode && blockVol.glusterBlockExecVolRes != nil {
		iscsiVol.Portals = blockVol.glusterBlockExecVolRes.Portals
		iscsiVol.Iqn = blockVol.glusterBlockExecVolRes.Iqn
		iscsiVol.User = blockVol.glusterBlockExecVolRes.User
		iscsiVol.AuthKey = blockVol.glusterBlockExecVolRes.AuthKey
		iscsiVol.BlockVolName = blockVolName
		iscsiVol.VolumeID = ""
	} else {
		return fmt.Errorf("failed to parse blockvol %v for opmode %v response", *blockVol, cfg.opMode)
	}
	return nil
}

//createSecretRef() creates a secret reference.
func (p *glusterBlockProvisioner) createSecretRef(nameSpace string, secretName string, user string, password string) (*v1.SecretReference, error) {
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

	secretRef := &v1.SecretReference{}
	if secret != nil {
		_, err = p.client.CoreV1().Secrets(nameSpace).Create(secret)
		if err != nil && errors.IsAlreadyExists(err) {

			glog.V(1).Infof("secret %s already exist in namespace %s", secret, nameSpace)
			err = nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create secret %s, %v", secret, err)
		}

		if secretRef != nil {
			secretRef.Name = secretName
			glog.V(1).Infof("secret %v and secretRef %v", secret, secretRef)
		}
	} else {
		return nil, fmt.Errorf("secret is nil")

	}
	return secretRef, nil
}

// createVolume creates a gluster block volume i.e. the storage asset.
func (p *glusterBlockProvisioner) createVolume(volSizeInt int, blockVol string, config *provisionerConfig) (*glusterBlockVolume, error) {

	blockRes := &glusterBlockVolume{}
	sizeStr := strconv.Itoa(volSizeInt)
	haCountStr := strconv.Itoa(config.haCount)

	glog.V(2).Infof("create block volume of size %d  and configuration %+v", volSizeInt, config)

	// Possible opModes are gluster-block and heketi:
	switch config.opMode {

	// An experimental/Test Mode:
	case glusterBlockOpmode:

		gBlockCreateErr := p.glusterBlockExecCreate(blockRes, config, sizeStr, haCountStr, blockVol)
		if gBlockCreateErr != nil {
			glog.Errorf("gluster block volume creation failed: %v", gBlockCreateErr)
			return nil, fmt.Errorf("gluster block volume creation failed: %v", gBlockCreateErr)
		}

	case heketiOpmode:
		hBlockCreateErr := p.heketiBlockVolCreate(blockRes, config, volSizeInt, haCountStr, blockVol)
		if hBlockCreateErr != nil {
			glog.Errorf("heketi block volume creation failed: %v", hBlockCreateErr)
			return nil, fmt.Errorf("heketi block volume creation failed: %v", hBlockCreateErr)
		}

	default:
		return nil, fmt.Errorf("error parsing value for 'opmode' for volume plugin %s", provisionerName)
	}
	return blockRes, nil
}

func (p *glusterBlockProvisioner) glusterBlockExecCreate(blockRes *glusterBlockVolume, config *provisionerConfig, sizeStr string, haCountStr string, blockVol string) error {

	if config == nil {
		return fmt.Errorf("provisioner config %v is nil", config)
	}

	blockRes.heketiBlockVolRes = nil

	if config.blockModeArgs["glustervol"] == "" {
		return fmt.Errorf("glustervol field of blockmodeargs is nil")
	}
	cmd := exec.Command(
		config.opMode, "create", config.blockModeArgs["glustervol"]+"/"+blockVol,
		"ha", haCountStr, config.blockModeArgs["hosts"], sizeStr+"GiB", "--json")

	out, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		glog.Errorf("command %v failed,%v", cmd, cmdErr)
		return fmt.Errorf("gluster block command %v failed: %v", cmd, cmdErr)
	}

	if blockRes == nil {
		return fmt.Errorf(" block response struct %v is  nil", blockRes)
	}

	// Fill the block configuration.
	execBlockRes := &blockRes.glusterBlockExecVolRes
	unmarshErr := json.Unmarshal([]byte(out), execBlockRes)
	if unmarshErr != nil {
		glog.Errorf("failed to unmarshal gluster-block command response: %v", unmarshErr)
		return fmt.Errorf("failed to unmarshal gluster-block command response: %v", unmarshErr)
	}

	//TODO: Do volume check before modify
	if config.chapAuthEnabled {
		cmd := exec.Command(
			config.opMode, "modify", config.blockModeArgs["glustervol"]+"/"+blockVol,
			"auth", "enable", "--json")

		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			glog.Errorf("error: %v when running command %v", cmdErr, cmd)
			return cmdErr
		}
		unmarshErr = json.Unmarshal([]byte(out), execBlockRes)
		if unmarshErr != nil {

			glog.Errorf("failed to unmarshal gluster-block command response: %v", unmarshErr)
			return fmt.Errorf("failed to unmarshal auth response from gluster-block command output: %v", unmarshErr)
		}
		if *execBlockRes == nil {
			return fmt.Errorf("failed to decode gluster-block response")
		}

		if config.chapAuthEnabled && ((**execBlockRes).User == "" || (**execBlockRes).AuthKey == "") {
			return fmt.Errorf("Invalid response from gluster-block received: CHAP credentials must not be empty")
		}

	}
	return nil
}

func (p *glusterBlockProvisioner) heketiBlockVolCreate(blockRes *glusterBlockVolume, config *provisionerConfig, volSizeInt int, haCountStr string, blockVol string) error {
	var clusterIDs []string
	var heketiBlockRes heketiBlockVolRes
	blockRes.glusterBlockExecVolRes = nil
	cli := gcli.NewClient(config.url, config.user, config.restSecretValue)
	if cli == nil {
		glog.Errorf("failed to create glusterblock REST client")
		return fmt.Errorf("failed to create glusterblock REST client, REST server authentication failed")
	}

	if config.clusterID != "" {
		clusterIDs = dstrings.Split(config.clusterID, ",")
		glog.V(4).Infof("provided clusterIDs %v", clusterIDs)
	}

	blockVolumeReq := &gapi.BlockVolumeCreateRequest{
		Size:     volSizeInt,
		Clusters: clusterIDs,
		Hacount:  config.haCount,
		Auth:     config.chapAuthEnabled,
		Name:     blockVol,
	}

	blockVolumeInfoRes, err := cli.BlockVolumeCreate(blockVolumeReq)

	if err != nil {
		glog.Errorf("[heketi] failed to create volume: %v", err)
		return fmt.Errorf("[heketi] failed to create volume: %v", err)

	}

	if blockVolumeInfoRes != nil {
		// Fill the params

		if blockVolumeInfoRes.BlockVolume.Iqn != "" && blockVolumeInfoRes.BlockVolume.Hosts[0] != "" {
			heketiBlockRes.Iqn = blockVolumeInfoRes.BlockVolume.Iqn
			heketiBlockRes.Portals = blockVolumeInfoRes.BlockVolume.Hosts
			heketiBlockRes.Lun = blockVolumeInfoRes.BlockVolume.Lun
			heketiBlockRes.User = blockVolumeInfoRes.BlockVolume.Username
			heketiBlockRes.AuthKey = blockVolumeInfoRes.BlockVolume.Password
			heketiBlockRes.Cluster = blockVolumeInfoRes.Cluster
			heketiBlockRes.ID = blockVolumeInfoRes.Id
		} else {
			return fmt.Errorf("[heketi] Invalid response from heketi received: IQN and Target must not be empty")
		}

		blockRes.heketiBlockVolRes = &heketiBlockRes

		if config.chapAuthEnabled && (heketiBlockRes.User == "" || heketiBlockRes.AuthKey == "") {
			return fmt.Errorf("[heketi] Invalid response from heketi received: CHAP credentials must not be empty  ")
		}

	} else {
		return fmt.Errorf("[heketi] blockvolumeinforesponse is nil ")
	}
	return nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *glusterBlockProvisioner) Delete(volume *v1.PersistentVolume) error {
	config := &provisionerConfig{}
	config.blockModeArgs = make(map[string]string)
	heketiModeArgs := make(map[string]string)
	ann, ok := volume.Annotations[provisionerIDAnn]
	if !ok {
		return fmt.Errorf("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{Reason: "identity annotation on PV does not match this provisioners identity"}
	}

	delBlockVolName, ok := volume.Annotations[shareIDAnn]
	if !ok {
		return fmt.Errorf("share annotation not found on PV")
	}

	delBlockString, ok := volume.Annotations["Blockstring"]
	delBlockStrSlice := dstrings.Split(delBlockString, ",")

	config.opMode = volume.Annotations[creatorAnn]
	for _, v := range delBlockStrSlice {
		if v != "" {
			s := dstrings.Split(v, ":")
			if config.opMode == glusterBlockOpmode {
				config.blockModeArgs[s[0]] = s[1]
			} else {
				if s[0] == "url" {
					heketiModeArgs[s[0]] = dstrings.Join(s[1:], ":")
				} else {
					heketiModeArgs[s[0]] = s[1]
				}

			}
		}
	}

	// Delete this blockVol
	glog.V(1).Infof("blockVolume %v to be deleted", delBlockVolName)

	//Call subjected volume delete operation.
	switch config.opMode {

	case glusterBlockOpmode:
		glog.V(1).Infof("Deleting Volume %v ", delBlockVolName)
		deleteCmd := exec.Command(
			config.opMode, "delete",
			config.blockModeArgs["glustervol"]+"/"+delBlockVolName, "--json")
		_, cmdErr := deleteCmd.CombinedOutput()
		if cmdErr != nil {
			glog.Errorf("error %v when running gluster-block command %v", cmdErr, deleteCmd)
			return cmdErr
		}
		glog.V(1).Infof("successfully deleted Volume %v", delBlockVolName)

	case heketiOpmode:

		glog.V(1).Infof("opmode[heketi]: deleting Volume %v", delBlockVolName)
		heketiModeArgs["restsecretvalue"] = ""
		if heketiModeArgs["secret"] != "" && heketiModeArgs["secretnamespace"] != "" {
			var err error
			heketiModeArgs["restsecretvalue"], err = parseSecret(heketiModeArgs["secretnamespace"], heketiModeArgs["secret"], p.client)
			if err != nil {
				glog.Errorf("[heketi]: failed to parse secret %s : Error, %v", heketiModeArgs["secret"], err)
				return err
			}
		}
		cli := gcli.NewClient(heketiModeArgs["url"], heketiModeArgs["user"], heketiModeArgs["restsecretvalue"])
		if cli == nil {
			glog.Errorf("[heketi]: failed to create REST client")
			return fmt.Errorf("[heketi]: failed to create REST client, REST server authentication failed")
		}

		volumeID, err := getVolumeID(volume, delBlockVolName)
		if err != nil {
			return fmt.Errorf("failed to get volumeID, err: %v", err)
		}

		deleteErr := cli.BlockVolumeDelete(volumeID)
		if deleteErr != nil {
			if dstrings.Contains(deleteErr.Error(), errIDNotFound) {
				glog.Errorf("[heketi]: failed to find volume ID %v in database, manual intervention required", volumeID)
				return fmt.Errorf("[heketi]: failed to find volume ID %v in database : %v", volumeID, deleteErr)
			}

			glog.Errorf("[heketi]: failed to delete gluster block volume %v: %v", delBlockVolName, deleteErr)
			return fmt.Errorf("[heketi]: failed to delete glusterblock volume %v: %v", delBlockVolName, deleteErr)
		}
		glog.V(1).Infof("[heketi]: successfully deleted Volume %v", delBlockVolName)

	default:
		glog.Errorf("Unknown OpMode, failed to delete volume %v", delBlockVolName)

	}

	if volume.Annotations["AccessKey"] != "" && volume.Annotations["AccessKeyNs"] != "" {
		deleteSecErr := p.client.CoreV1().Secrets(volume.Annotations["AccessKeyNs"]).Delete(volume.Annotations["AccessKey"], nil)

		if deleteSecErr != nil && errors.IsNotFound(deleteSecErr) {
			glog.V(1).Infof("secret %s does not exist in namespace %s", volume.Annotations["AccessKey"], volume.Annotations["AccessKeyNs"])
			deleteSecErr = nil
		}
		if deleteSecErr != nil {
			glog.Errorf("failed to delete secret %v/%v: %v", volume.Annotations["AccessKey"], volume.Annotations["AccessKeyNs"], deleteSecErr)
			return fmt.Errorf("failed to delete secret %v/%v: %v", volume.Annotations["AccessKey"], volume.Annotations["AccessKeyNs"], deleteSecErr)
		}
	}
	return nil
}

// getVolumeID returns volumeID from the PV or volumename.
func getVolumeID(pv *v1.PersistentVolume, volumeName string) (string, error) {
	volumeID := ""

	// Get volID from pvspec if available, else fill it from volumename.
	if pv != nil {
		if pv.Annotations[volIDAnn] != "" {
			volumeID = pv.Annotations[volIDAnn]
		} else {
			volumeID = dstrings.TrimPrefix(volumeName, blockVolPrefix)
		}
	} else {
		return volumeID, fmt.Errorf("provided PV spec is nil")
	}
	if volumeID == "" {
		return volumeID, fmt.Errorf("volume ID is empty")
	}
	return volumeID, nil
}

//sortTargetPortal extract TP
func (p *glusterBlockProvisioner) sortTargetPortal(vol *iscsiSpec) error {
	if len(vol.Portals) == 0 {
		return fmt.Errorf("portal is empty")
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
	return nil
}

func parseClassParameters(params map[string]string, kubeclient kubernetes.Interface) (*provisionerConfig, error) {
	var cfg provisionerConfig
	var err error

	//Set Defaults for args
	authEnabled := true
	chapAuthEnabled := true
	parseOpmode := ""
	blkmodeArgs := ""
	haCount := 3
	parseVolumeNamePrefix := ""

	for k, v := range params {
		switch dstrings.ToLower(k) {
		case "resturl":
			cfg.url = v
		case "restuser":
			cfg.user = v
		case "restsecretname":
			cfg.restSecretName = v
		case "restsecretnamespace":
			cfg.restSecretNamespace = v
		case "clusterids":
			if len(v) != 0 {
				cfg.clusterID = v
			}
		case "restauthenabled":
			authEnabled = dstrings.ToLower(v) == "true"
		case "hacount":
			haCount, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("failed to parse hacount %v: %v ", k, err)
			}
			cfg.haCount = haCount
		case "opmode":
			parseOpmode = v
		case "blockmodeargs":
			blkmodeArgs = v
		case "chapauthenabled":
			chapAuthEnabled = dstrings.ToLower(v) == "true"
		case "volumenameprefix":
			if len(v) != 0 {
				parseVolumeNamePrefix = v
			}
		default:
			return nil, fmt.Errorf("invalid option %q for volume plugin %s", k, "glusterblock")
		}
	}

	if len(parseOpmode) == 0 {
		cfg.opMode = heketiOpmode
	} else {
		parseErr := parseOpmodeArgs(parseOpmode, &cfg, blkmodeArgs)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing failed: %v", parseErr)
		}
	}

	if len(cfg.url) == 0 && cfg.opMode == heketiOpmode {
		return nil, fmt.Errorf("StorageClass for provisioner %s must contain 'resturl' parameter", "glusterblock")
	}

	if cfg.opMode == heketiOpmode {
		if !authEnabled {
			cfg.user = ""
			cfg.restSecretName = ""
			cfg.restSecretNamespace = ""
			cfg.restSecretValue = ""
		}

		if len(cfg.restSecretName) != 0 || len(cfg.restSecretNamespace) != 0 {
			if len(cfg.restSecretName) != 0 && len(cfg.restSecretNamespace) != 0 {
				cfg.restSecretValue, err = parseSecret(cfg.restSecretNamespace, cfg.restSecretName, kubeclient)
				if err != nil {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("StorageClass for provisioner %q must have restSecretNamespace and restSecretName either both set or both empty", "glusterblock")

			}
		} else if authEnabled {
			return nil, fmt.Errorf("`restauthenabled` should be set to false if `restsecret` and `restsecretnamespace` is nil")
		} else {
			glog.V(1).Infof("rest authentication is not enabled")
		}

	}

	cfg.restAuthEnabled = authEnabled
	cfg.chapAuthEnabled = chapAuthEnabled

	if len(parseVolumeNamePrefix) != 0 {
		if dstrings.Contains(parseVolumeNamePrefix, "_") {
			return nil, fmt.Errorf("Storageclass parameter 'volumenameprefix' should not contain '_' in its value")
		}
		cfg.volumeNamePrefix = parseVolumeNamePrefix
	}
	return &cfg, nil
}

func parseOpmodeArgs(parseOpmode string, cfg *provisionerConfig, blkmodeArgs string) error {
	switch parseOpmode {

	case glusterBlockOpmode:
		cfg.opMode = glusterBlockOpmode
		if len(blkmodeArgs) == 0 {
			return fmt.Errorf("[gluster-block] arg: %s has to be set if 'gluster-block' opmode is set", "blockmodeargs")
		}
		parseOpmodeInfo := dstrings.Split(blkmodeArgs, "=")
		if len(parseOpmodeInfo) >= 2 {
			argsDict, err := parseBlockModeArgs(cfg.opMode, blkmodeArgs)
			if err != nil {
				return fmt.Errorf("[gluster-block] failed to parse arguments: %v", err)
			}
			cfg.blockModeArgs = *argsDict
		} else {
			return fmt.Errorf("[gluster-block] wrong number of arguments for opmode %s", parseOpmode)
		}

	case heketiOpmode:
		cfg.opMode = heketiOpmode
	default:
		return fmt.Errorf("StorageClass for provisioner %s contains unknown %v parameter", "glusterblock", parseOpmode)
	}

	return nil
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
				return nil, fmt.Errorf("invalid parameter for %v", "glustervol")
			}
		case "hosts":
			blockHosts := dstrings.Split(v, "=")[1]
			if blockHosts != "" {
				modeArgs["hosts"] = blockHosts
			} else {
				return nil, fmt.Errorf("invalid  parameter for %v", "hosts")
			}
		default:
			return nil, fmt.Errorf("invalid parameter for %v", mode)
		}
	}
	return &modeArgs, nil
}

// parseSecret finds a given Secret instance and reads user password from it.
func parseSecret(namespace, secretName string, kubeClient kubernetes.Interface) (string, error) {

	secretMap, err := GetSecretForPV(namespace, secretName, provisionerName, kubeClient)
	if err != nil {
		glog.Errorf("failed to get secret [%s/%s], %v", namespace, secretName, err)
		return "", fmt.Errorf("failed to get secret [%s/%s], %v", namespace, secretName, err)
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
		glog.Fatalf("Failed to create kubernetes config: %v", err)
	}

	provName := provisionerName
	provEnvName := os.Getenv(provisionerNameKey)

	// Precedence is given for ProvisionerNameKey
	if provEnvName != "" && *id != "" {
		provName = provEnvName
	}

	if provEnvName == "" && *id != "" {
		provName = *id
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("failed to create kubernetes client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("failed to get server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	glusterBlockProvisioner := NewGlusterBlockProvisioner(clientset, provName)

	// Start the provision controller which will dynamically provision glusterblock
	// PVs

	pc := controller.NewProvisionController(
		clientset,
		provName,
		glusterBlockProvisioner,
		serverVersion.GitVersion,
		controller.Threadiness(2),
		controller.FailedProvisionThreshold(30),
	)

	pc.Run(wait.NeverStop)
}
