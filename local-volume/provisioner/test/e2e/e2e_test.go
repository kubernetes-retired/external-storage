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

package e2e

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	yaml "gopkg.in/yaml.v2"
	"k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/storage/utils"
)

const (
	hostBase = "/tmp"
	// Path to the first volume in the test containers
	// created via createLocalPod or makeLocalPod
	// leveraging pv_util.MakePod
	volumeDir = "/mnt/volume1"
	// testFile created in setupLocalVolume
	testFile = "test-file"
	// testFileContent written into testFile
	testFileContent = "test-file-content"
	testSCPrefix    = "local-volume-test-storageclass"

	// testServiceAccount is the service account for bootstrapper
	testServiceAccount = "local-storage-admin"
	// volumeConfigName is the configmap passed to bootstrapper and provisioner
	volumeConfigName = "local-volume-config"
	// provisioner image used for e2e tests
	provisionerImageName = "quay.io/external_storage/local-volume-provisioner:latest"
	// provisioner daemonSetName name
	daemonSetName = "local-volume-provisioner"
	// provisioner default mount point folder
	provisionerDefaultMountRoot = "/mnt/local-storage"
	// provisioner node/pv cluster role binding
	nodeBindingName         = "local-storage:provisioner-node-binding"
	pvBindingName           = "local-storage:provisioner-pv-binding"
	systemRoleNode          = "system:node"
	systemRolePVProvisioner = "system:persistent-volume-provisioner"

	// A sample request size
	testRequestSize = "10Mi"

	// Max number of nodes to use for testing
	maxNodes = 5
)

var (
	// storage class volume binding modes
	waitMode      = storagev1.VolumeBindingWaitForFirstConsumer
	immediateMode = storagev1.VolumeBindingImmediate
)

type localVolumeType string

const (
	// default local volume type, aka a directory
	DirectoryLocalVolumeType localVolumeType = "dir"
	// like DirectoryLocalVolumeType but it's a symbolic link to directory
	DirectoryLinkLocalVolumeType localVolumeType = "dir-link"
	// like DirectoryLocalVolumeType but bind mounted
	DirectoryBindMountedLocalVolumeType localVolumeType = "dir-bindmounted"
	// like DirectoryLocalVolumeType but it's a symbolic link to self bind mounted directory
	// Note that bind mounting at symbolic link actually mounts at directory it
	// links to.
	DirectoryLinkBindMountedLocalVolumeType localVolumeType = "dir-link-bindmounted"
	// creates a tmpfs and mounts it
	TmpfsLocalVolumeType localVolumeType = "tmpfs"
	// tests based on local ssd at /mnt/disks/by-uuid/
	GCELocalSSDVolumeType localVolumeType = "gce-localssd-scsi-fs"
	// Creates a local file, formats it, and maps it as a block device.
	BlockLocalVolumeType localVolumeType = "block"
	// Creates a local file serving as the backing for block device., formats it,
	// and mounts it to use as FS mode local volume.
	BlockFsWithFormatLocalVolumeType localVolumeType = "blockfswithformat"
	// Creates a local file serving as the backing for block device. do not format it manually,
	// and mounts it to use as FS mode local volume.
	BlockFsWithoutFormatLocalVolumeType localVolumeType = "blockfswithoutformat"
)

type localTestConfig struct {
	ns           string
	nodes        []v1.Node
	nodeExecPods map[string]*v1.Pod
	node0        *v1.Node
	client       clientset.Interface
	scName       string
	discoveryDir string
}

var _ = utils.SIGDescribe("PersistentVolumes-local ", func() {
	f := framework.NewDefaultFramework("persistent-local-volumes-test")
	var (
		config *localTestConfig
	)

	BeforeEach(func() {
		// Get all the schedulable nodes
		nodes := framework.GetReadySchedulableNodesOrDie(f.ClientSet)
		Expect(len(nodes.Items)).NotTo(BeZero(), "No available nodes for scheduling")

		// Cap max number of nodes
		maxLen := len(nodes.Items)
		if maxLen > maxNodes {
			maxLen = maxNodes
		}

		// Choose the first node
		node0 := &nodes.Items[0]

		config = &localTestConfig{
			ns:           f.Namespace.Name,
			client:       f.ClientSet,
			nodes:        nodes.Items[:maxLen],
			nodeExecPods: make(map[string]*v1.Pod, maxLen),
			node0:        node0,

			scName:       fmt.Sprintf("%v-%v", testSCPrefix, f.Namespace.Name),
			discoveryDir: filepath.Join(hostBase, f.Namespace.Name),
		}
	})

	Context("Local volume provisioner [Serial]", func() {
		var volumePath string

		BeforeEach(func() {
			setupStorageClass(config, &immediateMode)
			setupLocalVolumeProvisioner(config)
			volumePath = path.Join(config.discoveryDir, fmt.Sprintf("vol-%v", string(uuid.NewUUID())))
			setupLocalVolumeProvisionerMountPoint(config, volumePath, config.node0)
		})

		AfterEach(func() {
			cleanupLocalVolumeProvisionerMountPoint(config, volumePath, config.node0)
			cleanupLocalVolumeProvisioner(config)
			cleanupStorageClass(config)
		})

		It("should create and recreate local persistent volume", func() {
			By("Starting a provisioner daemonset")
			createProvisionerDaemonset(config)

			By("Waiting for a PersistentVolume to be created")
			oldPV, err := waitForLocalPersistentVolume(config.client, volumePath)
			Expect(err).NotTo(HaveOccurred())

			// Create a persistent volume claim for local volume: the above volume will be bound.
			By("Creating a persistent volume claim")
			claim, err := config.client.CoreV1().PersistentVolumeClaims(config.ns).Create(newLocalClaim(config))
			Expect(err).NotTo(HaveOccurred())
			err = framework.WaitForPersistentVolumeClaimPhase(
				v1.ClaimBound, config.client, claim.Namespace, claim.Name, framework.Poll, 1*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			claim, err = config.client.CoreV1().PersistentVolumeClaims(config.ns).Get(claim.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(claim.Spec.VolumeName).To(Equal(oldPV.Name))

			// Delete the persistent volume claim: file will be cleaned up and volume be re-created.
			By("Deleting the persistent volume claim to clean up persistent volume and re-create one")
			writeCmd := createWriteCmd(volumePath, testFile, testFileContent, DirectoryLocalVolumeType)
			err = issueNodeCommand(config, writeCmd, config.node0)
			Expect(err).NotTo(HaveOccurred())
			err = config.client.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(claim.Name, &metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for a new PersistentVolume to be re-created")
			newPV, err := waitForLocalPersistentVolume(config.client, volumePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(newPV.UID).NotTo(Equal(oldPV.UID))
			fileDoesntExistCmd := createFileDoesntExistCmd(volumePath, testFile)
			err = issueNodeCommand(config, fileDoesntExistCmd, config.node0)
			Expect(err).NotTo(HaveOccurred())

			By("Deleting provisioner daemonset")
			deleteProvisionerDaemonset(config)
		})
		It("should not create local persistent volume for filesystem volume that was not bind mounted", func() {

			directoryPath := filepath.Join(config.discoveryDir, "notbindmount")
			By("Creating a directory, not bind mounted, in discovery directory")
			mkdirCmd := fmt.Sprintf("mkdir -p %v -m 777", directoryPath)
			err := issueNodeCommand(config, mkdirCmd, config.node0)
			Expect(err).NotTo(HaveOccurred())

			By("Starting a provisioner daemonset")
			createProvisionerDaemonset(config)

			By("Allowing provisioner to run for 30s and discover potential local PVs")
			time.Sleep(30 * time.Second)

			By("Examining provisioner logs for not an actual mountpoint message")
			provisionerPodName := findProvisionerDaemonsetPodName(config)
			logs, err := framework.GetPodLogs(config.client, config.ns, provisionerPodName, "" /*containerName*/)
			Expect(err).NotTo(HaveOccurred(),
				"Error getting logs from pod %s in namespace %s", provisionerPodName, config.ns)

			expectedLogMessage := "Path \"/mnt/local-storage/notbindmount\" is not an actual mountpoint"
			Expect(strings.Contains(logs, expectedLogMessage)).To(BeTrue())

			By("Deleting provisioner daemonset")
			deleteProvisionerDaemonset(config)
		})
		It("should discover dynamically created local persistent volume mountpoint in discovery directory", func() {
			By("Starting a provisioner daemonset")
			createProvisionerDaemonset(config)

			By("Creating a volume in discovery directory")
			dynamicVolumePath := path.Join(config.discoveryDir, fmt.Sprintf("vol-%v", string(uuid.NewUUID())))
			setupLocalVolumeProvisionerMountPoint(config, dynamicVolumePath, config.node0)

			By("Waiting for the PersistentVolume to be created")
			_, err := waitForLocalPersistentVolume(config.client, dynamicVolumePath)
			Expect(err).NotTo(HaveOccurred())

			By("Deleting provisioner daemonset")
			deleteProvisionerDaemonset(config)

			By("Deleting volume in discovery directory")
			cleanupLocalVolumeProvisionerMountPoint(config, dynamicVolumePath, config.node0)
		})

	})
})

func setupStorageClass(config *localTestConfig, mode *storagev1.VolumeBindingMode) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.scName,
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		VolumeBindingMode: mode,
	}

	sc, err := config.client.StorageV1().StorageClasses().Create(sc)
	Expect(err).NotTo(HaveOccurred())
}

func cleanupStorageClass(config *localTestConfig) {
	framework.ExpectNoError(config.client.StorageV1().StorageClasses().Delete(config.scName, nil))
}

func setupLocalVolumeProvisioner(config *localTestConfig) {
	By("Bootstrapping local volume provisioner")
	createServiceAccount(config)
	createProvisionerClusterRoleBinding(config)
	utils.PrivilegedTestPSPClusterRoleBinding(config.client, config.ns, false /* teardown */, []string{testServiceAccount})
	createVolumeConfigMap(config)

	for _, node := range config.nodes {
		By(fmt.Sprintf("Initializing local volume discovery base path on node %v", node.Name))
		mkdirCmd := fmt.Sprintf("mkdir -p %v -m 777", config.discoveryDir)
		err := issueNodeCommand(config, mkdirCmd, &node)
		Expect(err).NotTo(HaveOccurred())
	}
}

func cleanupLocalVolumeProvisioner(config *localTestConfig) {
	By("Cleaning up cluster role binding")
	deleteClusterRoleBinding(config)
	utils.PrivilegedTestPSPClusterRoleBinding(config.client, config.ns, true /* teardown */, []string{testServiceAccount})

	for _, node := range config.nodes {
		By(fmt.Sprintf("Removing the test discovery directory on node %v", node.Name))
		removeCmd := fmt.Sprintf("[ ! -e %v ] || rm -r %v", config.discoveryDir, config.discoveryDir)
		err := issueNodeCommand(config, removeCmd, &node)
		Expect(err).NotTo(HaveOccurred())
	}
}

func createServiceAccount(config *localTestConfig) {
	serviceAccount := v1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: testServiceAccount, Namespace: config.ns},
	}
	_, err := config.client.CoreV1().ServiceAccounts(config.ns).Create(&serviceAccount)
	Expect(err).NotTo(HaveOccurred())
}

// createProvisionerClusterRoleBinding creates two cluster role bindings for local volume provisioner's
// service account: systemRoleNode and systemRolePVProvisioner. These are required for
// provisioner to get node information and create persistent volumes.
func createProvisionerClusterRoleBinding(config *localTestConfig) {
	subjects := []rbacv1beta1.Subject{
		{
			Kind:      rbacv1beta1.ServiceAccountKind,
			Name:      testServiceAccount,
			Namespace: config.ns,
		},
	}

	pvBinding := rbacv1beta1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1beta1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: pvBindingName,
		},
		RoleRef: rbacv1beta1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     systemRolePVProvisioner,
		},
		Subjects: subjects,
	}
	nodeBinding := rbacv1beta1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1beta1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeBindingName,
		},
		RoleRef: rbacv1beta1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     systemRoleNode,
		},
		Subjects: subjects,
	}

	deleteClusterRoleBinding(config)
	_, err := config.client.RbacV1beta1().ClusterRoleBindings().Create(&pvBinding)
	Expect(err).NotTo(HaveOccurred())
	_, err = config.client.RbacV1beta1().ClusterRoleBindings().Create(&nodeBinding)
	Expect(err).NotTo(HaveOccurred())
}

func deleteClusterRoleBinding(config *localTestConfig) {
	// These role bindings are created in provisioner; we just ensure it's
	// deleted and do not panic on error.
	config.client.RbacV1beta1().ClusterRoleBindings().Delete(nodeBindingName, metav1.NewDeleteOptions(0))
	config.client.RbacV1beta1().ClusterRoleBindings().Delete(pvBindingName, metav1.NewDeleteOptions(0))
}

func setupLocalVolumeProvisionerMountPoint(config *localTestConfig, volumePath string, node *v1.Node) {
	By(fmt.Sprintf("Creating local directory at path %q", volumePath))
	mkdirCmd := fmt.Sprintf("mkdir %v -m 777", volumePath)
	err := issueNodeCommand(config, mkdirCmd, node)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("Mounting local directory at path %q", volumePath))
	mntCmd := fmt.Sprintf("sudo mount --bind %v %v", volumePath, volumePath)
	err = issueNodeCommand(config, mntCmd, node)
	Expect(err).NotTo(HaveOccurred())
}

// launchNodeExecPodForLocalPV launches a hostexec pod for local PV and waits
// until it's Running.
func launchNodeExecPodForLocalPV(client clientset.Interface, ns, node string) *v1.Pod {
	hostExecPod := framework.NewHostExecPodSpec(ns, fmt.Sprintf("hostexec-%s", node))
	hostExecPod.Spec.NodeName = node
	hostExecPod.Spec.Volumes = []v1.Volume{
		{
			// Required to enter into host mount namespace via nsenter.
			Name: "rootfs",
			VolumeSource: v1.VolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
	}
	hostExecPod.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{
		{
			Name:      "rootfs",
			MountPath: "/rootfs",
			ReadOnly:  true,
		},
	}
	hostExecPod.Spec.Containers[0].SecurityContext = &v1.SecurityContext{
		Privileged: func(privileged bool) *bool {
			return &privileged
		}(true),
	}
	pod, err := client.CoreV1().Pods(ns).Create(hostExecPod)
	framework.ExpectNoError(err)
	err = framework.WaitForPodRunningInNamespace(client, pod)
	framework.ExpectNoError(err)
	return pod
}

// issueNodeCommandWithResult issues command on given node and returns stdout.
func issueNodeCommandWithResult(config *localTestConfig, cmd string, node *v1.Node) (string, error) {
	var pod *v1.Pod
	pod, ok := config.nodeExecPods[node.Name]
	if !ok {
		pod = launchNodeExecPodForLocalPV(config.client, config.ns, node.Name)
		if pod == nil {
			return "", fmt.Errorf("failed to create hostexec pod for node %q", node)
		}
		config.nodeExecPods[node.Name] = pod
	}
	args := []string{
		"exec",
		fmt.Sprintf("--namespace=%v", pod.Namespace),
		pod.Name,
		"--",
		"nsenter",
		"--mount=/rootfs/proc/1/ns/mnt",
		"--",
		"sh",
		"-c",
		cmd,
	}
	return framework.RunKubectl(args...)
}

// issueNodeCommand works like issueNodeCommandWithResult, but discards result.
func issueNodeCommand(config *localTestConfig, cmd string, node *v1.Node) error {
	_, err := issueNodeCommandWithResult(config, cmd, node)
	return err
}

func cleanupLocalVolumeProvisionerMountPoint(config *localTestConfig, volumePath string, node *v1.Node) {
	By(fmt.Sprintf("Unmounting the test mount point from %q", volumePath))
	umountCmd := fmt.Sprintf("[ ! -e %v ] || sudo umount %v", volumePath, volumePath)
	err := issueNodeCommand(config, umountCmd, node)
	Expect(err).NotTo(HaveOccurred())

	By("Removing the test mount point")
	removeCmd := fmt.Sprintf("[ ! -e %v ] || rm -r %v", volumePath, volumePath)
	err = issueNodeCommand(config, removeCmd, node)
	Expect(err).NotTo(HaveOccurred())

	By("Cleaning up persistent volume")
	pv, err := findLocalPersistentVolume(config.client, volumePath)
	Expect(err).NotTo(HaveOccurred())
	if pv != nil {
		err = config.client.CoreV1().PersistentVolumes().Delete(pv.Name, &metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
}

func createVolumeConfigMap(config *localTestConfig) {
	// MountConfig and ProvisionerConfiguration from
	// https://github.com/kubernetes-incubator/external-storage/blob/master/local-volume/provisioner/pkg/common/common.go
	type MountConfig struct {
		// The hostpath directory
		HostDir  string `json:"hostDir" yaml:"hostDir"`
		MountDir string `json:"mountDir" yaml:"mountDir"`
	}
	type ProvisionerConfiguration struct {
		// StorageClassConfig defines configuration of Provisioner's storage classes
		StorageClassConfig map[string]MountConfig `json:"storageClassMap" yaml:"storageClassMap"`
	}
	var provisionerConfig ProvisionerConfiguration
	provisionerConfig.StorageClassConfig = map[string]MountConfig{
		config.scName: {
			HostDir:  config.discoveryDir,
			MountDir: provisionerDefaultMountRoot,
		},
	}

	data, err := yaml.Marshal(&provisionerConfig.StorageClassConfig)
	Expect(err).NotTo(HaveOccurred())

	configMap := v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volumeConfigName,
			Namespace: config.ns,
		},
		Data: map[string]string{
			"storageClassMap": string(data),
		},
	}

	_, err = config.client.CoreV1().ConfigMaps(config.ns).Create(&configMap)
	Expect(err).NotTo(HaveOccurred())
}

// findLocalPersistentVolume finds persistent volume with 'spec.local.path' equals 'volumePath'.
func findLocalPersistentVolume(c clientset.Interface, volumePath string) (*v1.PersistentVolume, error) {
	pvs, err := c.CoreV1().PersistentVolumes().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, p := range pvs.Items {
		if p.Spec.PersistentVolumeSource.Local != nil && p.Spec.PersistentVolumeSource.Local.Path == volumePath {
			return &p, nil
		}
	}
	// Doesn't exist, that's fine, it could be invoked by early cleanup
	return nil, nil
}

func createProvisionerDaemonset(config *localTestConfig) {
	provisionerPrivileged := true
	mountProp := v1.MountPropagationHostToContainer

	provisioner := &extensionsv1beta1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "extensions/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: daemonSetName,
		},
		Spec: extensionsv1beta1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "local-volume-provisioner"},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "local-volume-provisioner"},
				},
				Spec: v1.PodSpec{
					ServiceAccountName: testServiceAccount,
					Containers: []v1.Container{
						{
							Name:            "provisioner",
							Image:           provisionerImageName,
							ImagePullPolicy: "Never", // Always use local image
							SecurityContext: &v1.SecurityContext{
								Privileged: &provisionerPrivileged,
							},
							Env: []v1.EnvVar{
								{
									Name: "MY_NODE_NAME",
									ValueFrom: &v1.EnvVarSource{
										FieldRef: &v1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name: "MY_NAMESPACE",
									ValueFrom: &v1.EnvVarSource{
										FieldRef: &v1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      volumeConfigName,
									MountPath: "/etc/provisioner/config/",
								},
								{
									Name:             "local-disks",
									MountPath:        provisionerDefaultMountRoot,
									MountPropagation: &mountProp,
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: volumeConfigName,
							VolumeSource: v1.VolumeSource{
								ConfigMap: &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{
										Name: volumeConfigName,
									},
								},
							},
						},
						{
							Name: "local-disks",
							VolumeSource: v1.VolumeSource{
								HostPath: &v1.HostPathVolumeSource{
									Path: config.discoveryDir,
								},
							},
						},
					},
				},
			},
		},
	}
	_, err := config.client.ExtensionsV1beta1().DaemonSets(config.ns).Create(provisioner)
	Expect(err).NotTo(HaveOccurred())

	kind := schema.GroupKind{Group: "extensions", Kind: "DaemonSet"}
	framework.WaitForControlledPodsRunning(config.client, config.ns, daemonSetName, kind)
}

// waitForLocalPersistentVolume waits a local persistent volume with 'volumePath' to be available.
func waitForLocalPersistentVolume(c clientset.Interface, volumePath string) (*v1.PersistentVolume, error) {
	var pv *v1.PersistentVolume

	for start := time.Now(); time.Since(start) < 10*time.Minute && pv == nil; time.Sleep(5 * time.Second) {
		pvs, err := c.CoreV1().PersistentVolumes().List(metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		if len(pvs.Items) == 0 {
			continue
		}
		for _, p := range pvs.Items {
			if p.Spec.PersistentVolumeSource.Local == nil || p.Spec.PersistentVolumeSource.Local.Path != volumePath {
				continue
			}
			if p.Status.Phase != v1.VolumeAvailable {
				continue
			}
			pv = &p
			break
		}
	}
	if pv == nil {
		return nil, fmt.Errorf("Timeout while waiting for local persistent volume with path %v to be available", volumePath)
	}
	return pv, nil
}

// newLocalClaim creates a new persistent volume claim.
func newLocalClaim(config *localTestConfig) *v1.PersistentVolumeClaim {
	claim := v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "local-pvc-",
			Namespace:    config.ns,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &config.scName,
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(testRequestSize),
				},
			},
		},
	}

	return &claim
}

func createWriteCmd(testDir string, testFile string, writeTestFileContent string, volumeType localVolumeType) string {
	if volumeType == BlockLocalVolumeType {
		// testDir is the block device.
		testFileDir := filepath.Join("/tmp", testDir)
		testFilePath := filepath.Join(testFileDir, testFile)
		// Create a file containing the testFileContent.
		writeTestFileCmd := fmt.Sprintf("mkdir -p %s; echo %s > %s", testFileDir, writeTestFileContent, testFilePath)
		// sudo is needed when using ssh exec to node.
		// sudo is not needed and does not exist in some containers (e.g. busybox), when using pod exec.
		sudoCmd := fmt.Sprintf("SUDO_CMD=$(which sudo); echo ${SUDO_CMD}")
		// Write the testFileContent into the block device.
		writeBlockCmd := fmt.Sprintf("${SUDO_CMD} dd if=%s of=%s bs=512 count=100", testFilePath, testDir)
		// Cleanup the file containing testFileContent.
		deleteTestFileCmd := fmt.Sprintf("rm %s", testFilePath)
		return fmt.Sprintf("%s && %s && %s && %s", writeTestFileCmd, sudoCmd, writeBlockCmd, deleteTestFileCmd)
	}
	testFilePath := filepath.Join(testDir, testFile)
	return fmt.Sprintf("mkdir -p %s; echo %s > %s", testDir, writeTestFileContent, testFilePath)
}

// Create command to verify that the file doesn't exist
// to be executed via hostexec Pod on the node with the local PV
func createFileDoesntExistCmd(testFileDir string, testFile string) string {
	testFilePath := filepath.Join(testFileDir, testFile)
	return fmt.Sprintf("[ ! -e %s ]", testFilePath)
}

func deleteProvisionerDaemonset(config *localTestConfig) {
	ds, err := config.client.ExtensionsV1beta1().DaemonSets(config.ns).Get(daemonSetName, metav1.GetOptions{})
	if ds == nil {
		return
	}

	err = config.client.ExtensionsV1beta1().DaemonSets(config.ns).Delete(daemonSetName, nil)
	Expect(err).NotTo(HaveOccurred())

	err = wait.PollImmediate(time.Second, time.Minute, func() (bool, error) {
		pods, err2 := config.client.CoreV1().Pods(config.ns).List(metav1.ListOptions{})
		if err2 != nil {
			return false, err2
		}

		for _, pod := range pods.Items {
			if metav1.IsControlledBy(&pod, ds) {
				// DaemonSet pod still exists
				return false, nil
			}
		}

		// All DaemonSet pods are deleted
		return true, nil
	})
	Expect(err).NotTo(HaveOccurred())
}

func findProvisionerDaemonsetPodName(config *localTestConfig) string {
	podList, err := config.client.CoreV1().Pods(config.ns).List(metav1.ListOptions{})
	if err != nil {
		framework.Failf("could not get the pod list: %v", err)
		return ""
	}
	pods := podList.Items
	for _, pod := range pods {
		if strings.HasPrefix(pod.Name, daemonSetName) && pod.Spec.NodeName == config.node0.Name {
			return pod.Name
		}
	}
	framework.Failf("Unable to find provisioner daemonset pod on node0")
	return ""
}

func TestE2E(t *testing.T) {
	RunE2ETests(t)
}
