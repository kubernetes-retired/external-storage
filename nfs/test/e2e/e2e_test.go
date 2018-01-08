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

package e2e

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/opencontainers/runc/libcontainer/selinux"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/v1"
	extensions "k8s.io/kubernetes/pkg/apis/extensions/v1beta1"
	"k8s.io/kubernetes/pkg/apis/storage/v1beta1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// StorageClassAnnotation represents the storage class associated with a resource.
// It currently matches the Beta value and can change when official is set.
// - in PersistentVolumeClaim it represents required class to match.
//   Only PersistentVolumes with the same class (i.e. annotation with the same
//   value) can be bound to the claim. In case no such volume exists, the
//   controller will provision a new one using StorageClass instance with
//   the same name as the annotation value.
// - in PersistentVolume it represents storage class to which the persistent
//   volume belongs.
//TODO: Update this to final annotation value as it matches BetaStorageClassAnnotation for now
const StorageClassAnnotation = "volume.beta.kubernetes.io/storage-class"

const (
	pluginName = "example.com/nfs"
	// Requested size of the volume
	requestedSize = "100Mi"
	// Expected size of the volume is the same, unlike cloud providers
	expectedSize = "100Mi"
)

func testDynamicProvisioning(client clientset.Interface, claim *v1.PersistentVolumeClaim) {
	pv := testCreate(client, claim)
	testWrite(client, claim)
	testRead(client, claim)
	testDelete(client, claim, pv)
}

func testCreate(client clientset.Interface, claim *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	err := framework.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, client, claim.Namespace, claim.Name, framework.Poll, 1*time.Minute)
	Expect(err).NotTo(HaveOccurred())

	By("checking the claim")
	// Get new copy of the claim
	claim, err = client.CoreV1().PersistentVolumeClaims(claim.Namespace).Get(claim.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Get the bound PV
	pv, err := client.CoreV1().PersistentVolumes().Get(claim.Spec.VolumeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Check sizes
	expectedCapacity := resource.MustParse(expectedSize)
	pvCapacity := pv.Spec.Capacity[v1.ResourceName(v1.ResourceStorage)]
	Expect(pvCapacity.Value()).To(Equal(expectedCapacity.Value()))

	requestedCapacity := resource.MustParse(requestedSize)
	claimCapacity := claim.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	Expect(claimCapacity.Value()).To(Equal(requestedCapacity.Value()))

	// Check PV properties
	Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(Equal(v1.PersistentVolumeReclaimDelete))
	expectedAccessModes := []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce}
	Expect(pv.Spec.AccessModes).To(Equal(expectedAccessModes))
	Expect(pv.Spec.ClaimRef.Name).To(Equal(claim.ObjectMeta.Name))
	Expect(pv.Spec.ClaimRef.Namespace).To(Equal(claim.ObjectMeta.Namespace))

	return pv
}

// We start two pods, first in testWrite and second in testRead:
// - The first writes 'hello word' to the /mnt/test (= the volume).
// - The second one runs grep 'hello world' on /mnt/test.
// If both succeed, Kubernetes actually allocated something that is
// persistent across pods.
func testWrite(client clientset.Interface, claim *v1.PersistentVolumeClaim) {
	By("checking the created volume is writable")
	runInPodWithVolume(client, claim.Namespace, claim.Name, "echo 'hello world' > /mnt/test/data")

	// Unlike cloud providers, kubelet should unmount NFS quickly
	By("Sleeping to let kubelet destroy all pods")
	time.Sleep(5 * time.Second)
}

func testRead(client clientset.Interface, claim *v1.PersistentVolumeClaim) {
	By("checking the created volume is readable and retains data")
	runInPodWithVolume(client, claim.Namespace, claim.Name, "grep 'hello world' /mnt/test/data")

	// Unlike cloud providers, kubelet should unmount NFS quickly
	By("Sleeping to let kubelet destroy all pods")
	time.Sleep(5 * time.Second)
}

func testDelete(client clientset.Interface, claim *v1.PersistentVolumeClaim, pv *v1.PersistentVolume) {
	By("deleting the claim")
	framework.ExpectNoError(client.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(claim.Name, nil))

	// Wait for the PV to get deleted too.
	framework.ExpectNoError(framework.WaitForPersistentVolumeDeleted(client, pv.Name, 5*time.Second, 1*time.Minute))
}

var _ = framework.KubeDescribe("Volumes [Feature:Volumes]", func() {
	f := framework.NewDefaultFramework("volume")
	// filled in BeforeEach
	var c clientset.Interface
	var ns string
	var pod *v1.Pod

	BeforeEach(func() {
		c = f.ClientSet
		ns = f.Namespace.Name
	})

	framework.KubeDescribe("Out-of-tree DynamicProvisioner nfs-provisioner", func() {
		AfterEach(func() {
			logs, err := framework.GetPodLogs(c, ns, pod.Name, pod.Spec.Containers[0].Name)
			if err != nil {
				framework.Logf("Error getting pod logs: %v", err)
			} else {
				framework.Logf("Pod logs:\n%s", logs)
			}
		})

		It("should create and delete persistent volumes [Slow]", func() {
			By("creating an out-of-tree dynamic provisioner pod")
			pod = startProvisionerPod(c, ns)
			defer c.Core().Pods(ns).Delete(pod.Name, nil)

			By("creating a StorageClass")
			class := newStorageClass()
			_, err := c.Storage().StorageClasses().Create(class)
			defer c.Storage().StorageClasses().Delete(class.Name, nil)
			Expect(err).NotTo(HaveOccurred())

			By("creating a claim with a dynamic provisioning annotation")
			claim := newClaim(ns)
			defer func() {
				c.Core().PersistentVolumeClaims(ns).Delete(claim.Name, nil)
			}()
			claim, err = c.Core().PersistentVolumeClaims(ns).Create(claim)
			Expect(err).NotTo(HaveOccurred())

			testDynamicProvisioning(c, claim)
		})
		It("should survive a restart [Slow]", func() {
			By("creating an out-of-tree dynamic provisioner deployment of 1 replica")
			service, deployment := startProvisionerDeployment(c, ns)
			defer c.Extensions().Deployments(ns).Delete(deployment.Name, nil)
			defer c.Core().Services(ns).Delete(service.Name, nil)
			pod = getDeploymentPod(c, ns, labels.Set(deployment.Spec.Selector.MatchLabels).String())

			By("creating a StorageClass")
			class := newStorageClass()
			_, err := c.Storage().StorageClasses().Create(class)
			defer c.Storage().StorageClasses().Delete(class.Name, nil)
			Expect(err).NotTo(HaveOccurred())

			By("creating a claim with a dynamic provisioning annotation")
			claim := newClaim(ns)
			defer func() {
				c.Core().PersistentVolumeClaims(ns).Delete(claim.Name, nil)
			}()
			claim, err = c.Core().PersistentVolumeClaims(ns).Create(claim)
			Expect(err).NotTo(HaveOccurred())

			pv := testCreate(c, claim)
			testWrite(c, claim)
			testRead(c, claim)

			By("scaling the deployment down to 0 then back to 1")
			// err = framework.ScaleDeployment(c, ns, deployment.Name, 0, false)
			// Expect(err).NotTo(HaveOccurred())
			scaleDeployment(c, ns, deployment.Name, 0)
			scaleDeployment(c, ns, deployment.Name, 1)
			pod = getDeploymentPod(c, ns, labels.Set(deployment.Spec.Selector.MatchLabels).String())

			testRead(c, claim)
			testDelete(c, claim, pv)
		})

	})
})

func newClaim(ns string) *v1.PersistentVolumeClaim {
	claim := v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pvc-",
			Namespace:    ns,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(requestedSize),
				},
			},
		},
	}

	claim.Annotations = map[string]string{
		StorageClassAnnotation: "fast",
	}

	return &claim
}

// runInPodWithVolume runs a command in a pod with given claim mounted to /mnt directory.
func runInPodWithVolume(c clientset.Interface, ns, claimName, command string) {
	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pvc-volume-tester-",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "volume-tester",
					Image:   "gcr.io/google_containers/busybox:1.24",
					Command: []string{"/bin/sh"},
					Args:    []string{"-c", command},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "my-volume",
							MountPath: "/mnt/test",
						},
					},
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
			Volumes: []v1.Volume{
				{
					Name: "my-volume",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: claimName,
							ReadOnly:  false,
						},
					},
				},
			},
		},
	}
	pod, err := c.Core().Pods(ns).Create(pod)
	defer func() {
		framework.ExpectNoError(c.Core().Pods(ns).Delete(pod.Name, nil))
	}()
	framework.ExpectNoError(err, "Failed to create pod: %v", err)
	framework.ExpectNoError(framework.WaitForPodSuccessInNamespace(c, pod.Name, pod.Namespace))
}

func newStorageClass() *v1beta1.StorageClass {
	return &v1beta1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind: "StorageClass",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "fast",
		},
		Provisioner: pluginName,
	}
}

func startProvisionerPod(c clientset.Interface, ns string) *v1.Pod {
	podClient := c.Core().Pods(ns)

	provisionerPod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "nfs-provisioner",
			Labels: map[string]string{
				"role": "nfs-provisioner",
			},
		},

		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nfs-provisioner",
					Image: "quay.io/kubernetes_incubator/nfs-provisioner:latest",
					SecurityContext: &v1.SecurityContext{
						Capabilities: &v1.Capabilities{
							Add: []v1.Capability{"DAC_READ_SEARCH", "SYS_RESOURCE"},
						},
					},
					Args: []string{
						fmt.Sprintf("-provisioner=%s", pluginName),
						"-grace-period=0",
					},
					Ports: []v1.ContainerPort{
						{Name: "nfs", ContainerPort: 2049},
						{Name: "mountd", ContainerPort: 20048},
						{Name: "rpcbind", ContainerPort: 111},
						{Name: "rpcbind-udp", ContainerPort: 111, Protocol: v1.ProtocolUDP},
					},
					Env: []v1.EnvVar{
						{
							Name: "POD_IP",
							ValueFrom: &v1.EnvVarSource{
								FieldRef: &v1.ObjectFieldSelector{
									FieldPath: "status.podIP",
								},
							},
						},
					},
					ImagePullPolicy: v1.PullIfNotPresent,
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "export-volume",
							MountPath: "/export",
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "export-volume",
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
	provisionerPod, err := podClient.Create(provisionerPod)
	framework.ExpectNoError(err, "Failed to create %s pod: %v", provisionerPod.Name, err)

	framework.ExpectNoError(framework.WaitForPodRunningInNamespace(c, provisionerPod))

	By("locating the provisioner pod")
	pod, err := podClient.Get(provisionerPod.Name, metav1.GetOptions{})
	framework.ExpectNoError(err, "Cannot locate the provisioner pod %v: %v", provisionerPod.Name, err)

	By("sleeping a bit to give the provisioner time to start")
	time.Sleep(5 * time.Second)
	return pod
}

func startProvisionerDeployment(c clientset.Interface, ns string) (*v1.Service, *extensions.Deployment) {
	gopath := os.Getenv("GOPATH")
	// TODO
	service := svcFromManifest(path.Join(gopath, "src/github.com/kubernetes-incubator/external-storage/nfs/deploy/kubernetes/deployment.yaml"))

	deployment := deployFromManifest(path.Join(gopath, "src/github.com/kubernetes-incubator/external-storage/nfs/deploy/kubernetes/deployment.yaml"))

	tmpDir, err := ioutil.TempDir("", "nfs-provisioner-deployment")
	Expect(err).NotTo(HaveOccurred())
	if selinux.SelinuxEnabled() {
		fcon, serr := selinux.Getfilecon(tmpDir)
		Expect(serr).NotTo(HaveOccurred())
		context := selinux.NewContext(fcon)
		context["type"] = "svirt_sandbox_file_t"
		serr = selinux.Chcon(tmpDir, context.Get(), false)
		Expect(serr).NotTo(HaveOccurred())
	}
	deployment.Spec.Template.Spec.Volumes[0].HostPath.Path = tmpDir
	deployment.Spec.Template.Spec.Containers[0].Image = "quay.io/kubernetes_incubator/nfs-provisioner:latest"
	deployment.Spec.Template.Spec.Containers[0].Args = []string{
		fmt.Sprintf("-provisioner=%s", pluginName),
		"-grace-period=10",
	}

	service, err = c.Core().Services(ns).Create(service)
	framework.ExpectNoError(err, "Failed to create %s service: %v", service.Name, err)

	deployment, err = c.Extensions().Deployments(ns).Create(deployment)
	framework.ExpectNoError(err, "Failed to create %s deployment: %v", deployment.Name, err)

	framework.ExpectNoError(framework.WaitForDeploymentStatus(c, deployment))

	By("sleeping a bit to give the provisioner time to start")
	time.Sleep(5 * time.Second)

	return service, deployment
}

func getDeploymentPod(c clientset.Interface, ns, labelSelector string) *v1.Pod {
	podList, err := c.CoreV1().Pods(ns).List(metav1.ListOptions{LabelSelector: labelSelector})
	Expect(err).NotTo(HaveOccurred())
	Expect(len(podList.Items)).Should(Equal(1))
	return &podList.Items[0]
}

// svcFromManifest reads a .json/yaml file and returns the json of the desired
func svcFromManifest(fileName string) *v1.Service {
	var service v1.Service
	data, err := ioutil.ReadFile(fileName)
	Expect(err).NotTo(HaveOccurred())

	r := ioutil.NopCloser(bytes.NewReader(data))
	decoder := utilyaml.NewDocumentDecoder(r)
	var chunk []byte
	for {
		chunk = make([]byte, len(data))
		_, err = decoder.Read(chunk)
		chunk = bytes.Trim(chunk, "\x00")
		Expect(err).NotTo(HaveOccurred())
		if strings.Contains(string(chunk), "kind: Service") {
			break
		}
	}

	json, err := utilyaml.ToJSON(chunk)
	Expect(err).NotTo(HaveOccurred())
	Expect(runtime.DecodeInto(api.Codecs.UniversalDecoder(), json, &service)).NotTo(HaveOccurred())

	return &service
}

// deployFromManifest reads a .json/yaml file and returns the json of the desired
func deployFromManifest(fileName string) *extensions.Deployment {
	var deployment extensions.Deployment
	data, err := ioutil.ReadFile(fileName)
	Expect(err).NotTo(HaveOccurred())

	r := ioutil.NopCloser(bytes.NewReader(data))
	decoder := utilyaml.NewDocumentDecoder(r)
	var chunk []byte
	for {
		chunk = make([]byte, len(data))
		_, err = decoder.Read(chunk)
		chunk = bytes.Trim(chunk, "\x00")
		Expect(err).NotTo(HaveOccurred())
		if strings.Contains(string(chunk), "kind: Deployment") {
			break
		}
	}

	json, err := utilyaml.ToJSON(chunk)
	Expect(err).NotTo(HaveOccurred())
	Expect(runtime.DecodeInto(api.Codecs.UniversalDecoder(), json, &deployment)).NotTo(HaveOccurred())

	return &deployment
}

func scaleDeployment(c clientset.Interface, ns, name string, newSize int32) {
	deployment, err := c.Extensions().Deployments(ns).Get(name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	deployment.Spec.Replicas = &newSize
	updatedDeployment, err := c.Extensions().Deployments(ns).Update(deployment)
	Expect(err).NotTo(HaveOccurred())
	framework.ExpectNoError(framework.WaitForDeploymentStatus(c, updatedDeployment))
	// Above is not enough. Just sleep to prevent conflict when doing Update.
	// kubectl Scaler would be ideal. or WaitForDeploymentStatus
	time.Sleep(5 * time.Second)
}
