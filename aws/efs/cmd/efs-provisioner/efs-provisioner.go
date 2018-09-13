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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/efs"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/gidallocator"
	"github.com/kubernetes-incubator/external-storage/lib/mount"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	provisionerNameKey = "PROVISIONER_NAME"
	fileSystemIDKey    = "FILE_SYSTEM_ID"
	awsRegionKey       = "AWS_REGION"
)

type efsProvisioner struct {
	dnsName    string
	mountpoint string
	source     string
	allocator  gidallocator.Allocator
}

// NewEFSProvisioner creates an AWS EFS volume provisioner
func NewEFSProvisioner(client kubernetes.Interface) controller.Provisioner {
	fileSystemID := os.Getenv(fileSystemIDKey)
	if fileSystemID == "" {
		glog.Fatalf("environment variable %s is not set! Please set it.", fileSystemIDKey)
	}

	awsRegion := os.Getenv(awsRegionKey)
	if awsRegion == "" {
		glog.Fatalf("environment variable %s is not set! Please set it.", awsRegionKey)
	}

	dnsName := getDNSName(fileSystemID, awsRegion)

	mountpoint, source, err := getMount(dnsName)
	if err != nil {
		glog.Fatal(err)
	}

	sess, err := session.NewSession()
	if err != nil {
		glog.Warningf("couldn't create an AWS session: %v", err)
	}

	svc := efs.New(sess, &aws.Config{Region: aws.String(awsRegion)})
	params := &efs.DescribeFileSystemsInput{
		FileSystemId: aws.String(fileSystemID),
	}

	_, err = svc.DescribeFileSystems(params)
	if err != nil {
		glog.Warningf("couldn't confirm that the EFS file system exists: %v", err)
	}

	return &efsProvisioner{
		dnsName:    dnsName,
		mountpoint: mountpoint,
		source:     source,
		allocator:  gidallocator.New(client),
	}
}

func getDNSName(fileSystemID, awsRegion string) string {
	return fileSystemID + ".efs." + awsRegion + ".amazonaws.com"
}

func getMount(dnsName string) (string, string, error) {
	entries, err := mount.GetMounts()
	if err != nil {
		return "", "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Source, dnsName) {
			return e.Mountpoint, e.Source, nil
		}
	}

	entriesStr := ""
	for _, e := range entries {
		entriesStr += e.Source + ":" + e.Mountpoint + ", "
	}
	return "", "", fmt.Errorf("no mount entry found for %s among entries %s", dnsName, entriesStr)
}

var _ controller.Provisioner = &efsProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *efsProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	gidAllocate := true
	for k, v := range options.Parameters {
		switch strings.ToLower(k) {
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
			return nil, err
		}
		gid = &allocate
	}

	err := p.createVolume(p.getLocalPath(options), gid)
	if err != nil {
		return nil, err
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   p.dnsName,
					Path:     p.getRemotePath(options),
					ReadOnly: false,
				},
			},
			MountOptions: []string{"vers=4.1"},
		},
	}
	if gidAllocate {
		pv.ObjectMeta.Annotations = map[string]string{
			gidallocator.VolumeGidAnnotationKey: strconv.FormatInt(int64(*gid), 10),
		}
	}

	return pv, nil
}

func (p *efsProvisioner) createVolume(path string, gid *int) error {
	perm := os.FileMode(0777)
	if gid != nil {
		perm = os.FileMode(0771 | os.ModeSetgid)
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}

	// Due to umask, need to chmod
	if err := os.Chmod(path, perm); err != nil {
		os.RemoveAll(path)
		return err
	}

	if gid != nil {
		cmd := exec.Command("chgrp", strconv.Itoa(*gid), path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			os.RemoveAll(path)
			return fmt.Errorf("chgrp failed with error: %v, output: %s", err, out)
		}
	}

	return nil
}

func (p *efsProvisioner) getLocalPath(options controller.VolumeOptions) string {
	return path.Join(p.mountpoint, p.getDirectoryName(options))
}

func (p *efsProvisioner) getRemotePath(options controller.VolumeOptions) string {
	sourcePath := path.Clean(strings.Replace(p.source, p.dnsName+":", "", 1))
	return path.Join(sourcePath, p.getDirectoryName(options))
}

func (p *efsProvisioner) getDirectoryName(options controller.VolumeOptions) string {
	return options.PVC.Name + "-" + options.PVName
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *efsProvisioner) Delete(volume *v1.PersistentVolume) error {
	//TODO ignorederror
	err := p.allocator.Release(volume)
	if err != nil {
		return err
	}

	path, err := p.getLocalPathToDelete(volume.Spec.NFS)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(path); err != nil {
		return err
	}

	return nil
}

func (p *efsProvisioner) getLocalPathToDelete(nfs *v1.NFSVolumeSource) (string, error) {
	if nfs.Server != p.dnsName {
		return "", fmt.Errorf("volume's NFS server %s is not equal to the server %s from which this provisioner creates volumes", nfs.Server, p.dnsName)
	}

	sourcePath := path.Clean(strings.Replace(p.source, p.dnsName+":", "", 1))
	if !strings.HasPrefix(nfs.Path, sourcePath) {
		return "", fmt.Errorf("volume's NFS path %s is not a child of the server path %s mounted in this provisioner at %s", nfs.Path, p.source, p.mountpoint)
	}

	subpath := strings.Replace(nfs.Path, sourcePath, "", 1)

	return path.Join(p.mountpoint, subpath), nil
}

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	// Create an InClusterConfig and use it to create a client for the controller
	// to use to communicate with Kubernetes
	config, err := rest.InClusterConfig()
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
	efsProvisioner := NewEFSProvisioner(clientset)

	provisionerName := os.Getenv(provisionerNameKey)
	if provisionerName == "" {
		glog.Fatalf("environment variable %s is not set! Please set it.", provisionerNameKey)
	}

	// Start the provision controller which will dynamically provision efs NFS
	// PVs
	pc := controller.NewProvisionController(
		clientset,
		provisionerName,
		efsProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
