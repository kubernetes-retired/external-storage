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

package volumeservice

import (
	"fmt"
	"io/ioutil"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	AuthURL        = "http://keystone-host:5000/v2.0"
	CinderEndpoint = "http://cinder-host:8776/v2"
)

var _ = Describe("Volume Service", func() {
	Describe("Connection configuration parsing", func() {
		var (
			config cinderConfig
			err    error
		)

		Context("when config file path is empty", func() {
			BeforeEach(func() {
				os.Setenv("OS_AUTH_URL", AuthURL)
				os.Setenv("OS_CINDER_ENDPOINT", CinderEndpoint)
				os.Setenv("OS_USERNAME", "USERNAME")
				os.Setenv("OS_PASSWORD", "PASSWORD")
				os.Setenv("OS_TENANT_ID", "TENANT_ID")
				os.Setenv("OS_REGION_NAME", "REGION_NAME")
				os.Setenv("OS_USER_DOMAIN_NAME", "USER_DOMAIN_NAME")
			})

			JustBeforeEach(func() {
				config, err = getConfig("")
			})

			It("should take the configuration from the environment", func() {
				ValidateConfig(config)
			})
		})

		Context("when config file path is specified", func() {
			var (
				tmpFile string
			)

			BeforeEach(func() {
				var file *os.File
				data := []byte(fmt.Sprintf(`
					[Global]
					auth-url=%s
					cinder-endpoint=%s
				`, AuthURL, CinderEndpoint))

				file, err = ioutil.TempFile(os.TempDir(), "cinderConfig")
				Expect(err).To(BeNil())
				tmpFile = file.Name()
				err = ioutil.WriteFile(tmpFile, data, 0644)
				Expect(err).To(BeNil())

			})
			AfterEach(func() {
				os.Remove(tmpFile)
			})

			JustBeforeEach(func() {
				config, err = getConfig(tmpFile)
			})

			It("should read the configuration from the file", func() {
				ValidateConfig(config)
			})

			Context("when the config file path does not exist", func() {
				BeforeEach(func() {
					os.Remove(tmpFile)
				})
				It("should fail", func() {
					Expect(err).NotTo(BeNil())
				})
			})
		})
	})
})

func ValidateConfig(config cinderConfig) {
	Expect(config.Global.AuthURL).To(Equal(AuthURL))
	Expect(config.Global.CinderEndpoint).To(Equal(CinderEndpoint))
}
