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

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/api/v1/helper"
)

const (
	provisionerName    = "ceph.com/cephfs"
	provisionCmd       = "/usr/local/bin/cephfs_provisioner"
	provisionerIDAnn   = "cephFSProvisionerIdentity"
	cephShareAnn       = "cephShare"
	provisionerNameKey = "PROVISIONER_NAME"
)

type provisionOutput struct {
	Path   string `json:"path"`
	User   string `json:"user"`
	Secret string `json:"auth"`
}

type cephFSProvisioner struct {
	// Kubernetes Client. Use to retrieve Ceph admin secret
	client kubernetes.Interface
	// Identity of this cephFSProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string
}

func newCephFSProvisioner(client kubernetes.Interface, id string) controller.Provisioner {
	return &cephFSProvisioner{
		client:   client,
		identity: id,
	}
}

var _ controller.Provisioner = &cephFSProvisioner{}

func generateSecretName(user string) string {
	return "ceph-" + user + "-secret"
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *cephFSProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}
	cluster, adminID, adminSecret, mon, err := p.parseParameters(options.Parameters)
	if err != nil {
		return nil, err
	}
	// create random share name
	share := fmt.Sprintf("kubernetes-dynamic-pvc-%s", uuid.NewUUID())
	// create random user id
	user := fmt.Sprintf("kubernetes-dynamic-user-%s", uuid.NewUUID())
	// provision share
	// create cmd
	cmd := exec.Command(provisionCmd, "-n", share, "-u", user)
	// set env
	cmd.Env = []string{
		"CEPH_CLUSTER_NAME=" + cluster,
		"CEPH_MON=" + strings.Join(mon[:], ","),
		"CEPH_AUTH_ID=" + adminID,
		"CEPH_AUTH_KEY=" + adminSecret}

	output, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		glog.Errorf("failed to provision share %q for %q, err: %v, output: %v", share, user, cmdErr, string(output))
		return nil, cmdErr
	}
	// validate output
	res := &provisionOutput{}
	json.Unmarshal([]byte(output), &res)
	if res.User == "" || res.Secret == "" || res.Path == "" {
		return nil, fmt.Errorf("invalid provisioner output")
	}
	// create secret in PVC's namespace
	nameSpace := options.PVC.Namespace
	secretName := generateSecretName(user)
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nameSpace,
			Name:      secretName,
		},
		Data: map[string][]byte{
			"key": []byte(res.Secret),
		},
		Type: "Opaque",
	}

	_, err = p.client.Core().Secrets(nameSpace).Create(secret)
	if err != nil {
		glog.Errorf("Cephfs Provisioner: create volume failed, err: %v", err)
		return nil, fmt.Errorf("failed to create secret")
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn: p.identity,
				cephShareAnn:     share,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{ //FIXME: kernel cephfs doesn't enforce quota, capacity is not meaningless here.
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CephFS: &v1.CephFSPersistentVolumeSource{
					Monitors: mon,
					Path:     res.Path[strings.Index(res.Path, "/"):],
					SecretRef: &v1.SecretReference{
						Name: secretName,
						// TODO https://github.com/kubernetes-incubator/external-storage/issues/309
						Namespace: nameSpace,
					},
					User: user,
				},
			},
		},
	}

	glog.Infof("successfully created CephFS share %+v", pv.Spec.PersistentVolumeSource.CephFS)

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *cephFSProvisioner) Delete(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[provisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{Reason: "identity annotation on PV does not match ours"}
	}
	share, ok := volume.Annotations[cephShareAnn]
	if !ok {
		return errors.New("ceph share annotation not found on PV")
	}
	// delete CephFS
	// TODO when beta is removed, have to check kube version and pick v1/beta
	// accordingly: maybe the controller lib should offer a function for that
	class, err := p.client.StorageV1beta1().StorageClasses().Get(helper.GetPersistentVolumeClass(volume), metav1.GetOptions{})
	if err != nil {
		return err
	}
	cluster, adminID, adminSecret, mon, err := p.parseParameters(class.Parameters)
	if err != nil {
		return err
	}
	user := volume.Spec.PersistentVolumeSource.CephFS.User
	// create cmd
	cmd := exec.Command(provisionCmd, "-r", "-n", share, "-u", user)
	// set env
	cmd.Env = []string{
		"CEPH_CLUSTER_NAME=" + cluster,
		"CEPH_MON=" + strings.Join(mon[:], ","),
		"CEPH_AUTH_ID=" + adminID,
		"CEPH_AUTH_KEY=" + adminSecret}

	output, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		glog.Errorf("failed to delete share %q for %q, err: %v, output: %v", share, user, cmdErr, string(output))
		return cmdErr
	}

	// Remove dynamic user secret
	secretName := generateSecretName(user)
	secretNamespace := volume.Spec.PersistentVolumeSource.CephFS.SecretRef.Namespace
	err = p.client.Core().Secrets(secretNamespace).Delete(secretName, &metav1.DeleteOptions{})
	if err != nil {
		glog.Errorf("Cephfs Provisioner: delete secret failed, err: %v", err)
		return fmt.Errorf("failed to delete secret")
	}

	return nil
}

func (p *cephFSProvisioner) parseParameters(parameters map[string]string) (string, string, string, []string, error) {
	var (
		err                                                                  error
		mon                                                                  []string
		cluster, adminID, adminSecretName, adminSecretNamespace, adminSecret string
	)

	adminSecretNamespace = "default"
	adminID = "admin"
	cluster = "ceph"

	for k, v := range parameters {
		switch strings.ToLower(k) {
		case "cluster":
			cluster = v
		case "monitors":
			arr := strings.Split(v, ",")
			for _, m := range arr {
				mon = append(mon, m)
			}
		case "adminid":
			adminID = v
		case "adminsecretname":
			adminSecretName = v
		case "adminsecretnamespace":
			adminSecretNamespace = v
		default:
			return "", "", "", nil, fmt.Errorf("invalid option %q", k)
		}
	}
	// sanity check
	if adminSecretName == "" {
		return "", "", "", nil, fmt.Errorf("missing Ceph admin secret name")
	}
	if adminSecret, err = p.parsePVSecret(adminSecretNamespace, adminSecretName); err != nil {
		return "", "", "", nil, fmt.Errorf("failed to get admin secret from [%q/%q]: %v", adminSecretNamespace, adminSecretName, err)
	}
	if len(mon) < 1 {
		return "", "", "", nil, fmt.Errorf("missing Ceph monitors")
	}
	return cluster, adminID, adminSecret, mon, nil
}

func (p *cephFSProvisioner) parsePVSecret(namespace, secretName string) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("Cannot get kube client")
	}
	secrets, err := p.client.Core().Secrets(namespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	for _, data := range secrets.Data {
		return string(data), nil
	}

	// If not found, the last secret in the map wins as done before
	return "", fmt.Errorf("no secret found")
}

var (
	master     = flag.String("master", "", "Master URL")
	kubeconfig = flag.String("kubeconfig", "", "Absolute path to the kubeconfig")
	id         = flag.String("id", "", "Unique provisioner identity")
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

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

	prName := provisionerName
	prNameFromEnv := os.Getenv(provisionerNameKey)
	if prNameFromEnv != "" {
		prName = prNameFromEnv
	}

	// By default, we use provisioner name as provisioner identity.
	// User may specify their own identity with `-id` flag to distinguish each
	// others, if they deploy more than one CephFS provisioners under same provisioner name.
	prID := prName
	if *id != "" {
		prID = *id
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		glog.Fatalf("Error getting server version: %v", err)
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	glog.Infof("Creating CephFS provisioner %s with identity: %s", prName, prID)
	cephFSProvisioner := newCephFSProvisioner(clientset, prID)

	// Start the provision controller which will dynamically provision cephFS
	// PVs
	pc := controller.NewProvisionController(
		clientset,
		prName,
		cephFSProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
