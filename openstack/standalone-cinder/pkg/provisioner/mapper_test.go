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

package provisioner

import (
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ = Describe("Mapper", func() {
	Describe("creating a volume mapper from cinder connection information", func() {
		var (
			conn   volumeservice.VolumeConnection
			mapper volumeMapper
			err    error
		)

		JustBeforeEach(func() {
			mapper, err = newVolumeMapperFromConnection(conn)
		})

		Context("when the connection type is iscsi", func() {
			BeforeEach(func() {
				conn = volumeservice.VolumeConnection{
					DriverVolumeType: iscsiType,
				}
			})

			It("should yield an iscsiMapper", func() {
				Expect(mapper).Should(BeAssignableToTypeOf(&iscsiMapper{}))
			})
		})

		Context("when the connection type is rbd", func() {
			BeforeEach(func() {
				conn = volumeservice.VolumeConnection{
					DriverVolumeType: rbdType,
				}
			})

			It("should yield an rbdMapper", func() {
				Expect(mapper).Should(BeAssignableToTypeOf(&rbdMapper{}))
			})
		})

		Context("when the connection type is not supported", func() {
			BeforeEach(func() {
				conn = volumeservice.VolumeConnection{
					DriverVolumeType: "Unsupported",
				}
			})

			It("should yield nil", func() {
				Expect(mapper).Should(BeNil())
			})

			It("should produce an error", func() {
				Expect(err).Should(Not(BeNil()))
			})
		})
	})

	Describe("creating a volume mapper from a PV", func() {
		var (
			pv     v1.PersistentVolume
			source v1.PersistentVolumeSource
			mapper volumeMapper
			err    error
		)

		BeforeEach(func() {
			pv = v1.PersistentVolume{
				Spec: v1.PersistentVolumeSpec{},
			}
		})

		JustBeforeEach(func() {
			pv.Spec.PersistentVolumeSource = source
			mapper, err = newVolumeMapperFromPV(&pv)
		})

		Context("with an ISCSI PV", func() {
			BeforeEach(func() {
				source = v1.PersistentVolumeSource{
					ISCSI: &v1.ISCSIPersistentVolumeSource{},
				}
			})

			It("should yield an iscsiMapper", func() {
				Expect(mapper).Should(BeAssignableToTypeOf(&iscsiMapper{}))
			})
		})

		Context("with an RBD PV", func() {
			BeforeEach(func() {
				source = v1.PersistentVolumeSource{
					RBD: &v1.RBDPersistentVolumeSource{},
				}
			})

			It("should yield an iscsiMapper", func() {
				Expect(mapper).Should(BeAssignableToTypeOf(&rbdMapper{}))
			})
		})

		Context("with an unsupported persistent volume source", func() {
			BeforeEach(func() {
				source = v1.PersistentVolumeSource{}
			})

			It("should yield nil", func() {
				Expect(mapper).Should(BeNil())
			})

			It("should produce an error", func() {
				Expect(err).Should(Not(BeNil()))
			})
		})
	})

	Describe("Building a persistent volume", func() {
		var (
			mapper *fakeMapper
			pv     *v1.PersistentVolume
			err    error
		)
		p := createCinderProvisioner()
		options := createVolumeOptions()
		conn := volumeservice.VolumeConnection{}
		volumeID := "volumeid"

		BeforeEach(func() {
			mapper = &fakeMapper{}
		})

		JustBeforeEach(func() {
			pv, err = buildPV(mapper, p, options, conn, volumeID)
		})

		Context("when building the PV source fails", func() {
			BeforeEach(func() {
				mapper.failBuildPVSource = true
			})
			It("should also fail", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
			})
		})

		It("should yield a valid PV associated with this provisioner", func() {
			annotations := map[string]string{
				ProvisionerIDAnn: "identity",
				CinderVolumeID:   "volumeid",
			}
			accessModes := []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce}
			quantity, parseErr := resource.ParseQuantity("1Gi")
			Expect(parseErr).To(BeNil())
			capacity := v1.ResourceList{
				v1.ResourceStorage: quantity,
			}

			Expect(pv).To(Not(BeNil()))
			Expect(err).To(BeNil())
			Expect(pv.Name).To(Equal("testpv"))
			Expect(pv.Namespace).To(Equal("testns"))
			Expect(pv.Annotations).To(Equal(annotations))
			Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(Equal(v1.PersistentVolumeReclaimDelete))
			Expect(pv.Spec.AccessModes).To(Equal(accessModes))
			Expect(pv.Spec.Capacity).To(Equal(capacity))
			Expect(pv.Spec.PersistentVolumeSource).To(Not(BeNil()))
		})
	})
})
