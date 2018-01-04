package volumeservice_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestVolumeservice(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Volumeservice Suite")
}
