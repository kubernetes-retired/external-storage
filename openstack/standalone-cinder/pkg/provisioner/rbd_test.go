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

var _ = Describe("Rbd Mapper", func() {
	Describe("Parsing ceph monitor information", func() {
		var (
			conn     volumeservice.VolumeConnection
			monitors []string
		)

		BeforeEach(func() {
			conn = createIscsiConnectionInfo()
		})

		JustBeforeEach(func() {
			monitors = getMonitors(conn)
		})

		Context("when no monitors are given", func() {
			It("should return an empty list", func() {
				Expect(monitors).To(BeEmpty())
			})
		})

		Context("when the number of hosts and ports are not equal", func() {
			BeforeEach(func() {
				conn.Data.Hosts = []string{"hostA"}
				conn.Data.Ports = []string{"1", "2"}
			})

			It("should return nil", func() {
				Expect(monitors).To(BeNil())
			})
		})

		Context("when the lists are of equal length", func() {
			BeforeEach(func() {
				conn.Data.Hosts = []string{"hostA", "hostB"}
				conn.Data.Ports = []string{"1", "2"}
			})

			It("should merge them into a single list", func() {
				expected := []string{"hostA:1", "hostB:2"}
				Expect(monitors).To(Equal(expected))
			})
		})
	})

	Describe("the persistent volume source", func() {
		var (
			mapper  rbdMapper
			conn    volumeservice.VolumeConnection
			options controller.VolumeOptions
			source  *v1.PersistentVolumeSource
			err     error
		)

		BeforeEach(func() {
			conn = createRbdConnectionInfo()
			options = createVolumeOptions()
			mapper = rbdMapper{}
		})

		JustBeforeEach(func() {
			source, err = mapper.BuildPVSource(conn, options)
		})

		Context("when the connection information is valid", func() {
			BeforeEach(func() {
				conn.Data.Hosts = []string{"hostA", "hostB"}
				conn.Data.Ports = []string{"1", "2"}
				conn.Data.Name = "pool/image"
			})

			It("should be populated with the RBD connection info", func() {
				expectedMonitors := []string{"hostA:1", "hostB:2"}
				Expect(source.RBD.CephMonitors).To(Equal(expectedMonitors))
				Expect(source.RBD.RBDPool).To(Equal("pool"))
				Expect(source.RBD.RBDImage).To(Equal("image"))
				Expect(source.RBD.RadosUser).To(Equal("admin"))
				Expect(source.RBD.SecretRef.Name).To(Equal("storageclass-cephx-secret"))
			})
		})

		Context("when the connection field 'name' cannot be split into pool and image", func() {
			BeforeEach(func() {
				conn.Data.Name = "has-no-slash"
			})

			It("should be nil", func() {
				Expect(source).To(BeNil())
			})
		})

		Context("when no monitors can be parsed", func() {
			BeforeEach(func() {
				conn.Data.Hosts = []string{"hostA", "hostB"}
				conn.Data.Ports = []string{"1"}
			})

			It("should be nil", func() {
				Expect(source).To(BeNil())
			})
		})

		Context("When the connection field 'name' contains an extra '/'", func() {
			BeforeEach(func() {
				conn.Data.Name = "pool/image/foo"
			})

			It("should have an image field containing a '/'", func() {
				Expect(source.RBD.RBDImage).To(Equal("image/foo"))
			})
		})
	})

	Describe("Authorization", func() {
		var (
			mapper rbdMapper
			err    error
		)

		Context("when called to setup", func() {
			It("should do nothing and always succeed", func() {
				err = mapper.AuthSetup(&cinderProvisioner{}, controller.VolumeOptions{},
					volumeservice.VolumeConnection{})
				Expect(err).To(BeNil())
			})
		})

		Context("when called to tear down", func() {
			It("should do nothing and always succeed", func() {
				err = mapper.AuthTeardown(&cinderProvisioner{}, &v1.PersistentVolume{})
				Expect(err).To(BeNil())
			})
		})
	})
})

func createRbdConnectionInfo() volumeservice.VolumeConnection {
	return volumeservice.VolumeConnection{
		DriverVolumeType: rbdType,
		Data: volumeservice.VolumeConnectionDetails{
			AuthUsername: "admin",
		},
	}
}
