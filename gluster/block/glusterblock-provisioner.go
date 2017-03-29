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
	"errors"
	"flag"
	"fmt"
	"os"
	exec "os/exec"
	"strconv"
	dstrings "strings"
	"time"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	//storage "k8s.io/client-go/pkg/apis/storage/v1beta1"
)

const (
	resyncPeriod              = 15 * time.Second
	provisionerName           = "gluster.org/glusterblock"
	exponentialBackOffOnError = false
	failedRetryThreshold      = 5
	defaultExecPath           = "./createiscsi"
	secretKeyName             = "key"
	leasePeriod               = controller.DefaultLeaseDuration
	retryPeriod               = controller.DefaultRetryPeriod
	renewDeadline             = controller.DefaultRenewDeadline
	termLimit                 = controller.DefaultTermLimit
)

type glusterBlockProvisioner struct {
	// Kubernetes Client. Use to retrieve Gluster admin secret
	client kubernetes.Interface

	// Identity of this glusterBlockProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity types.UID

	// Configuration of gluster block provisioner
	provConfig provisionerConfig
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

	// Optinal:  ClusterID from which the provisioner create the block volume
	clusterId string

	// Optional: high availability count in case of multipathing
	haCount int

	// Optional: Operation mode  (rest, executable)
	opMode string

	// Optional: Executable path if we are operating in executable mode.
	execPath string
}

func NewglusterBlockProvisioner(client kubernetes.Interface) controller.Provisioner {
	return &glusterBlockProvisioner{
		client:   client,
		identity: uuid.NewUUID(),
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

	// If we want to retrieve storage class name.
	// scName := storageutil.GetClaimStorageClass(r.options.PVC)

	cfg, err := parseClassParameters(options.Parameters, p.client)
	if err != nil {
		return nil, err
	}
	p.provConfig = *cfg

	glog.V(4).Infof("glusterfs: creating volume with configuration %+v", p.provConfig)

	target, iqn, err := p.createVolume(options.PVName)
	if err != nil {
		return nil, err
	}
	glog.V(1).Infof("Target Portal and IQN returned :%v %v", target, iqn)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				"glusterBlockProvisionerIdentity": string(p.identity),
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
					TargetPortal: target,
					IQN:          iqn,
					Lun:          0,
					FSType:       "ext4",
					ReadOnly:     false,
				},
			},
		},
	}
	glog.Infof("successfully created Gluster Block volume %+v", pv.Spec.PersistentVolumeSource.ISCSI)
	return pv, nil
}

// createVolume creates a gluster block volume i.e. the storage asset.

func (p *glusterBlockProvisioner) createVolume(PVName string) (string, string, error) {
	var dtarget, diqn string
	if p.provConfig.opMode == "executable" {
		cmd := exec.Command("sh", p.provConfig.execPath)
		err := cmd.Run()
		if err != nil {
			glog.Errorf("%v", err)
		}
		dtarget = os.Getenv("TARGET")
		diqn = os.Getenv("IQN")
	}
	return dtarget, diqn, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *glusterBlockProvisioner) Delete(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations["glusterBlockProvisionerIdentity"]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != string(p.identity) {
		return &controller.IgnoredError{"identity annotation on PV does not match ours"}
	}
	// Unset the variables.
	os.Setenv("TARGET", "")
	os.Setenv("IQN", "")

	return nil
}

func parseClassParameters(params map[string]string, kubeclient kubernetes.Interface) (*provisionerConfig, error) {
	var cfg provisionerConfig
	var err error

	authEnabled := true

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
		case "clusterid":
			if len(v) != 0 {
				cfg.clusterId = v
			}
		case "restauthenabled":
			authEnabled = dstrings.ToLower(v) == "true"
		case "hacount":
			haCount, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("glusterblock: failed to parse hacount %v ", k)
			}
		case "opmode":
			cfg.opMode = v
		case "execpath":
			cfg.execPath = v
		default:
			return nil, fmt.Errorf("glusterblock: invalid option %q for volume plugin %s", k, "glusterblock")
		}
	}

	if len(cfg.url) == 0 {
		return nil, fmt.Errorf("StorageClass for provisioner %s must contain 'resturl' parameter", "glusterblock")
	}

	if haCount == 0 {
		cfg.haCount = haCount
	}

	if len(cfg.opMode) == 0 {
		cfg.opMode = "executable"
	}

	if len(cfg.execPath) == 0 {
		cfg.execPath = defaultExecPath
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

// parseSecret finds a given Secret instance and reads user password from it.
func parseSecret(namespace, secretName string, kubeClient kubernetes.Interface) (string, error) {

	secretMap, err := GetSecretForPV(namespace, secretName, "glusterblock", kubeClient)
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
	glusterBlockProvisioner := NewglusterBlockProvisioner(clientset)

	// Start the provision controller which will dynamically provision glusterblock
	// PVs

	pc := controller.NewProvisionController(clientset, resyncPeriod, provisionerName, glusterBlockProvisioner, serverVersion.GitVersion, exponentialBackOffOnError, failedRetryThreshold, leasePeriod, renewDeadline, retryPeriod, termLimit)

	pc.Run(wait.NeverStop)
}
