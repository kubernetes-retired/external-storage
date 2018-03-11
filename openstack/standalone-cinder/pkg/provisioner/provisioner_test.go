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
	"bytes"
	"errors"

	"github.com/gophercloud/gophercloud"
	volumes_v2 "github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/openstack/standalone-cinder/pkg/volumeservice"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Provisioner", func() {
	Describe("Create volume options parsing", func() {
		var (
			err           error
			options       controller.VolumeOptions
			createOptions volumes_v2.CreateOpts
		)
		BeforeEach(func() {
			options = createVolumeOptions()
		})
		JustBeforeEach(func() {
			createOptions, err = getCreateOptions(options)
		})

		Context("when an unrecognized option is specified in the storage class", func() {
			BeforeEach(func() {
				options.Parameters = map[string]string{
					"foo": "bar",
				}
			})

			It("should fail", func() {
				Expect(createOptions).To(Equal(volumes_v2.CreateOpts{}))
				Expect(err).ToNot(BeNil())
			})
		})

		Context("when recognized options are used", func() {
			BeforeEach(func() {
				options.Parameters = map[string]string{
					"type":         "gold",
					"availability": "zone",
				}
			})

			It("should be reflected in the create options", func() {
				Expect(err).To(BeNil())
				Expect(createOptions.AvailabilityZone).To(Equal("zone"))
				Expect(createOptions.VolumeType).To(Equal("gold"))
			})
		})
	})

	Describe("A provision operation", func() {
		var (
			pv      *v1.PersistentVolume
			p       *cinderProvisioner
			vsb     *fakeVolumeServiceBroker
			mb      *fakeMapperBroker
			options controller.VolumeOptions
			cleanup string
			err     error
		)

		BeforeEach(func() {
			vsb = &fakeVolumeServiceBroker{}
			mb = newFakeMapperBroker()
			p = createCinderProvisioner()
			p.vsb = vsb
			p.mb = mb
			options = createVolumeOptions()
		})

		JustBeforeEach(func() {
			pv, err = p.Provision(options)
		})

		It("should return a persistent volume", func() {
			Expect(pv).To(Not(BeNil()))
			Expect(err).To(BeNil())
		})

		Context("when a claim selector is specified", func() {
			BeforeEach(func() {
				options.PVC.Spec.Selector = &metav1.LabelSelector{}
			})

			It("should fail", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when an unrecognized option is specified in the storage class", func() {
			BeforeEach(func() {
				options.Parameters = map[string]string{
					"foo": "bar",
				}
			})

			It("should fail", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when creating a volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("createCinderVolume")
			})
			It("should fail", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when the volume does not become available", func() {
			BeforeEach(func() {
				vsb.mightFail.set("waitForAvailableCinderVolume")
				cleanup = "deleteCinderVolume."
			})
			It("should fail and delete the volume", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})

		Context("when reserving the volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("reserveCinderVolume")
				cleanup = "deleteCinderVolume."
			})
			It("should fail and delete the volume", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})

		Context("when connecting the volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("connectCinderVolume")
				cleanup = "unreserveCinderVolume.deleteCinderVolume."
			})
			It("should fail and the volume should be unreserved and deleted", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})

		Context("when getting a volumeMapper fails", func() {
			BeforeEach(func() {
				mb.mightFail.set("newVolumeMapperFromConnection")
				cleanup = "disconnectCinderVolume.unreserveCinderVolume.deleteCinderVolume."
			})
			It("should fail and the volume should be disconnected, unreserved and deleted", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})

		Context("when preparing volume authentication fails", func() {
			BeforeEach(func() {
				mb.FakeVolumeMapper.mightFail.set("AuthSetup")
				cleanup = "disconnectCinderVolume.unreserveCinderVolume.deleteCinderVolume."
			})
			It("should fail and the volume should be disconnected, unreserved and deleted", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})

		Context("when building the PV fails", func() {
			BeforeEach(func() {
				mb.mightFail.set("buildPV")
				cleanup = "disconnectCinderVolume.unreserveCinderVolume.deleteCinderVolume."
			})
			It("should fail and the volume should be disconnected, unreserved and deleted", func() {
				Expect(pv).To(BeNil())
				Expect(err).To(Not(BeNil()))
				Expect(vsb.cleanupLog.String()).To(Equal(cleanup))
			})
		})
	})

	Describe("A delete operation", func() {
		var (
			err error
			vsb *fakeVolumeServiceBroker
			mb  *fakeMapperBroker
			p   *cinderProvisioner
			pv  *v1.PersistentVolume
		)

		BeforeEach(func() {
			vsb = &fakeVolumeServiceBroker{}
			mb = newFakeMapperBroker()
			p = createCinderProvisioner()
			p.vsb = vsb
			p.mb = mb
			pv = createPersistentVolume()
		})

		JustBeforeEach(func() {
			err = p.Delete(pv)
		})

		It("should complete successfully", func() {
			Expect(err).To(BeNil())
		})

		Context("when the provisioner ID annotation is missing from the PV", func() {
			BeforeEach(func() {
				delete(pv.Annotations, ProvisionerIDAnn)
			})

			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when the provisioner ID annotation does not match our provisioner", func() {
			BeforeEach(func() {
				pv.Annotations[ProvisionerIDAnn] = "a different provisioner"
			})

			It("should fail with an IgnoredError", func() {
				Expect(err).To(Not(BeNil()))
				Expect(err).To(BeAssignableToTypeOf(&controller.IgnoredError{}))
			})
		})

		Context("when the cinder volume ID annotation is missing from the PV", func() {
			BeforeEach(func() {
				delete(pv.Annotations, CinderVolumeID)
			})

			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when getting a volumeMapper fails", func() {
			BeforeEach(func() {
				mb.mightFail.set("newVolumeMapperFromPV")
			})
			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when teardown of volume authentication fails", func() {
			BeforeEach(func() {
				mb.FakeVolumeMapper.mightFail.set("AuthTeardown")
			})
			It("should still succeed", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("when disconnecting the volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("disconnectCinderVolume")
			})
			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when unreserving the volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("unreserveCinderVolume")
			})
			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("when deleting the volume fails", func() {
			BeforeEach(func() {
				vsb.mightFail.set("deleteCinderVolume")
			})
			It("should fail", func() {
				Expect(err).To(Not(BeNil()))
			})
		})
	})
})

type fakeVolumeServiceBroker struct {
	cleanupLog bytes.Buffer
	mightFail  failureInjector
	volumeServiceBroker
}

func (vsb *fakeVolumeServiceBroker) createCinderVolume(vs *gophercloud.ServiceClient, options volumes_v2.CreateOpts) (string, error) {
	if vsb.mightFail.isSet("createCinderVolume") {
		return "", errors.New("injected error for testing")
	}
	return "cinderVolumeID", nil
}

func (vsb *fakeVolumeServiceBroker) waitForAvailableCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return vsb.mightFail.ret("waitForAvailableCinderVolume")
}

func (vsb *fakeVolumeServiceBroker) reserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return vsb.mightFail.ret("reserveCinderVolume")
}

func (vsb *fakeVolumeServiceBroker) connectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) (volumeservice.VolumeConnection, error) {
	if vsb.mightFail.isSet("connectCinderVolume") {
		return volumeservice.VolumeConnection{}, errors.New("injected error for testing")
	}
	return volumeservice.VolumeConnection{}, nil
}

func (vsb *fakeVolumeServiceBroker) _cleanupRet(fn string) error {
	if vsb.mightFail.isSet(fn) {
		return errors.New("injected error for testing")
	}
	vsb.cleanupLog.WriteString(fn)
	vsb.cleanupLog.WriteString(".")
	return nil
}

func (vsb *fakeVolumeServiceBroker) disconnectCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return vsb._cleanupRet("disconnectCinderVolume")
}

func (vsb *fakeVolumeServiceBroker) unreserveCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return vsb._cleanupRet("unreserveCinderVolume")
}

func (vsb *fakeVolumeServiceBroker) deleteCinderVolume(vs *gophercloud.ServiceClient, volumeID string) error {
	return vsb._cleanupRet("deleteCinderVolume")
}

type fakeMapperBroker struct {
	mightFail        failureInjector
	FakeVolumeMapper *fakeMapper

	mapperBroker
}

func newFakeMapperBroker() *fakeMapperBroker {
	ret := fakeMapperBroker{}
	ret.FakeVolumeMapper = &fakeMapper{}
	return &ret
}

func (mb *fakeMapperBroker) newVolumeMapperFromConnection(conn volumeservice.VolumeConnection) (volumeMapper, error) {
	if mb.mightFail.isSet("newVolumeMapperFromConnection") {
		return nil, errors.New("injected error for testing")
	}
	return mb.FakeVolumeMapper, nil
}

func (mb *fakeMapperBroker) newVolumeMapperFromPV(pv *v1.PersistentVolume) (volumeMapper, error) {
	if mb.mightFail.isSet("newVolumeMapperFromPV") {
		return nil, errors.New("injected error for testing")
	}
	return mb.FakeVolumeMapper, nil
}

func (mb *fakeMapperBroker) buildPV(m volumeMapper, p *cinderProvisioner, options controller.VolumeOptions, conn volumeservice.VolumeConnection, volumeID string) (*v1.PersistentVolume, error) {
	if mb.mightFail.isSet("buildPV") {
		return nil, errors.New("injected error for testing")
	}
	return &v1.PersistentVolume{}, nil
}
