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

package e2e

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	// constants must match test setup.
	localVolumeNS = "kube-system"
	daemonSetName = "local-volume-provisioner"
	discoveryDir  = "/tmp/local-disks"
	requestedSize = "10Mi"
	testSC        = "local-storage"
	testVolume    = "vol1"
	testFile      = "file"
)

var r *rand.Rand

var _ = framework.KubeDescribe("[Volume] PersistentVolumes-local [Feature:LocalPersistentVolumes]", func() {
	f := framework.NewDefaultFramework("persistent-local-volumes-test")
	var c clientset.Interface
	var ns string

	BeforeEach(func() {
		c = f.ClientSet
		ns = f.Namespace.Name
	})

	framework.KubeDescribe("Local persistent volume provisioner", func() {
		It("should be successfully launched via bootstrapper", func() {
			kind := schema.GroupKind{"extensions", "DaemonSet"}
			framework.WaitForControlledPodsRunning(c, localVolumeNS, daemonSetName, kind)
		})

		It("should create a local persistent volume", func() {
			// Create a directory under discovery path: we should see an available local volume.
			volPath := path.Join(discoveryDir, testVolume+string(uuid.NewUUID()))
			err := os.Mkdir(volPath, os.FileMode(uint(0777)))
			Expect(err).NotTo(HaveOccurred())
			err = waitForLocalPersistentVolume(c, volPath)
			Expect(err).NotTo(HaveOccurred())

			// Create a persistent volume claim for local volume: the above volume will be bound.
			// Note: make sure no other PVs are available; otherwise, the test can be flaky.
			claim, err := c.Core().PersistentVolumeClaims(ns).Create(newClaim(ns))
			Expect(err).NotTo(HaveOccurred())
			err = framework.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, c, claim.Namespace, claim.Name, framework.Poll, 1*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			// Delete the persistent volume claim: file will be cleaned up and volume be re-created.
			file := path.Join(volPath, testFile)
			_, err = os.Create(file)
			Expect(err).NotTo(HaveOccurred())
			err = c.Core().PersistentVolumeClaims(claim.Namespace).Delete(claim.Name, &metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			err = waitForLocalPersistentVolume(c, volPath)
			Expect(err).NotTo(HaveOccurred())
			_, err = os.Stat(file)
			Expect(os.IsNotExist(err)).To(BeTrue())

			// FIXME: Delete the path should delete the PV?
			// pv, err := findLocalPersistentVolume(c, volPath)
			// Expect(err).NotTo(HaveOccurred())
			// err = os.Remove(volPath)
			// Expect(err).NotTo(HaveOccurred())
			// err = framework.WaitForPersistentVolumeDeleted(c, pv.Name, framework.Poll, 2*time.Minute)
			// Expect(err).NotTo(HaveOccurred())
			pv, err := findLocalPersistentVolume(c, volPath)
			Expect(err).NotTo(HaveOccurred())
			err = c.Core().PersistentVolumes().Delete(pv.Name, &metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// waitForLocalPersistentVolume waits a local persistent volume with 'volPath' to be available.
func waitForLocalPersistentVolume(c clientset.Interface, volPath string) error {
	available := false
	for start := time.Now(); time.Since(start) < 10*time.Minute && !available; time.Sleep(5 * time.Second) {
		pvs, err := c.Core().PersistentVolumes().List(metav1.ListOptions{})
		if err != nil {
			return err
		}
		if len(pvs.Items) == 0 {
			continue
		}
		for _, p := range pvs.Items {
			if p.Spec.PersistentVolumeSource.Local == nil || p.Spec.PersistentVolumeSource.Local.Path != volPath {
				continue
			}
			if p.Status.Phase != v1.VolumeAvailable {
				continue
			}
			available = true
			break
		}
	}
	if !available {
		return fmt.Errorf("Timeout while waiting for local persistent volume with path %v to be available", volPath)
	}
	return nil
}

// findLocalPersistentVolume finds persistent volume with 'spec.local.path' equals 'volPath'.
func findLocalPersistentVolume(c clientset.Interface, volPath string) (*v1.PersistentVolume, error) {
	pvs, err := c.Core().PersistentVolumes().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, p := range pvs.Items {
		if p.Spec.PersistentVolumeSource.Local != nil && p.Spec.PersistentVolumeSource.Local.Path == volPath {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("Unable to find local persistent volume with path %v", volPath)
}

// newClaim creates a new persistent volume claim.
func newClaim(ns string) *v1.PersistentVolumeClaim {
	sc := testSC
	claim := v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "local-pvc-",
			Namespace:    ns,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &sc,
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

	return &claim
}
