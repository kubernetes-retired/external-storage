package e2e

import (
	"github.com/kubernetes-incubator/nfs-provisioner/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func init() {
	framework.ViperizeFlags()
}

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2e Suite")
}
