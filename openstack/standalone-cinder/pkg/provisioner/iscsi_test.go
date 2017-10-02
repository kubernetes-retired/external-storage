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
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/api/core/v1"
)

var _ = Describe("Iscsi Mapper", func() {
	Describe("the CHAP secret name", func() {
		var (
			secretName string
			conn       volumeservice.VolumeConnection
		)
		options := createVolumeOptions()

		JustBeforeEach(func() {
			secretName = getChapSecretName(conn, options)
		})

		Context("when the authentication method is CHAP", func() {
			BeforeEach(func() {
				conn.Data.AuthMethod = "CHAP"
			})
			It("should be based on the PV name", func() {
				Expect(secretName).To(Equal("testpv-secret"))
			})
		})

		Context("when the authentication method is not CHAP", func() {
			BeforeEach(func() {
				conn.Data.AuthMethod = "not chap"
			})
			It("should be blank", func() {
				Expect(secretName).To(Equal(""))
			})
		})
	})

	Describe("the persistent volume source", func() {
		var (
			mapper     iscsiMapper
			err        error
			source     *v1.PersistentVolumeSource
			secretName string
			conn       volumeservice.VolumeConnection
		)
		options := controller.VolumeOptions{
			PVName: "myPV",
		}

		BeforeEach(func() {
			conn = createIscsiConnectionInfo()
			mapper = iscsiMapper{}
		})

		JustBeforeEach(func() {
			secretName = getChapSecretName(conn, options)
			source, err = mapper.BuildPVSource(conn, options)
		})

		It("should be populated with iscsi connection info", func() {
			Expect(source.ISCSI.TargetPortal).To(Equal("portal"))
			Expect(source.ISCSI.IQN).To(Equal("iqn"))
			Expect(source.ISCSI.Lun).To(Equal(int32(3)))
			Expect(source.ISCSI.SessionCHAPAuth).To(BeFalse())
		})

		Context("when CHAP authentication is enabled", func() {
			BeforeEach(func() {
				conn.Data.AuthMethod = "CHAP"
			})
			It("should contain a reference to a CHAP secret", func() {
				Expect(source.ISCSI.SessionCHAPAuth).To(BeTrue())
				Expect(source.ISCSI.SecretRef).To(Not(BeNil()))
				Expect(source.ISCSI.SecretRef.Name).To(Equal(secretName))
			})
		})
	})

	Describe("Authorization setup", func() {
		var (
			cb      *fakeClusterBroker
			mapper  iscsiMapper
			conn    volumeservice.VolumeConnection
			options controller.VolumeOptions
			err     error
		)

		BeforeEach(func() {
			cb = &fakeClusterBroker{}
			mapper = iscsiMapper{cb: cb}
			conn = createIscsiConnectionInfo()
			options = createVolumeOptions()
		})

		JustBeforeEach(func() {
			err = mapper.AuthSetup(&cinderProvisioner{}, options, conn)
		})

		Context("when the connection supplies CHAP credentials", func() {
			BeforeEach(func() {
				conn.Data.AuthMethod = "CHAP"
				conn.Data.AuthUsername = "user"
				conn.Data.AuthPassword = "pass"
			})

			It("should create a CHAP secret", func() {
				Expect(cb.CreatedSecret).To(Not(BeNil()))
				Expect(cb.CreatedSecret.Type).To(Equal(v1.SecretType("kubernetes.io/iscsi-chap")))
				Expect(cb.CreatedSecret.Name).To(Equal("testpv-secret"))
				user := string(cb.CreatedSecret.Data["node.session.auth.username"][:])
				pass := string(cb.CreatedSecret.Data["node.session.auth.password"][:])
				Expect(user).To(Equal("user"))
				Expect(pass).To(Equal("pass"))
			})

			It("should target the namespace where the PVC resides", func() {
				Expect(cb.Namespace).To(Equal(options.PVC.Namespace))
			})
		})

		Context("when the connection does not require CHAP authentication", func() {
			It("should not create a CHAP secret", func() {
				Expect(cb.CreatedSecret).To(BeNil())
			})
		})
	})

	Describe("Authorization Teardown", func() {
		var (
			cb     *fakeClusterBroker
			mapper iscsiMapper
			pv     *v1.PersistentVolume
			err    error
		)

		BeforeEach(func() {
			pv = createPersistentVolume()
			pv.Spec.PersistentVolumeSource.ISCSI = &v1.ISCSIVolumeSource{}
		})

		JustBeforeEach(func() {
			cb = &fakeClusterBroker{}
			mapper = iscsiMapper{cb: cb}
			err = mapper.AuthTeardown(&cinderProvisioner{}, pv)
		})

		Context("when the PV contains a secret reference", func() {
			BeforeEach(func() {
				pv.Spec.ISCSI.SecretRef = &v1.LocalObjectReference{
					Name: "secretName",
				}
			})

			It("should delete the secret", func() {
				Expect(cb.DeletedSecret).To(Equal("secretName"))
				Expect(cb.Namespace).To(Equal("testNs"))
			})
		})

		Context("when the PV does not contain a secret reference", func() {
			It("should not delete any secret", func() {
				Expect(cb.DeletedSecret).To(BeEmpty())
			})
		})
	})
})

func createIscsiConnectionInfo() volumeservice.VolumeConnection {
	return volumeservice.VolumeConnection{
		DriverVolumeType: iscsiType,
		Data: volumeservice.VolumeConnectionDetails{
			TargetPortal: "portal",
			TargetIqn:    "iqn",
			TargetLun:    3,
		},
	}
}

type fakeClusterBroker struct {
	clusterBroker
	CreatedSecret *v1.Secret
	DeletedSecret string
	Namespace     string
}

func (cb *fakeClusterBroker) createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error {
	cb.CreatedSecret = secret
	cb.Namespace = ns
	return nil
}

func (cb *fakeClusterBroker) deleteSecret(p *cinderProvisioner, ns string, secretName string) error {
	cb.DeletedSecret = secretName
	cb.Namespace = ns
	return nil
}
