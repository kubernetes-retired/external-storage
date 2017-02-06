/*
Copyright 2015 The Kubernetes Authors.

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

package framework

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	apierrs "k8s.io/client-go/pkg/api/errors"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apimachinery/registered"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	maxKubectlExecRetries = 5
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
// Eventual goal is to merge this with integration test framework.
type Framework struct {
	BaseName string

	ClientSet clientset.Interface

	ClientPool dynamic.ClientPool

	Namespace                *v1.Namespace   // Every test has at least one namespace
	namespacesToDelete       []*v1.Namespace // Some tests have more than one.
	NamespaceDeletionTimeout time.Duration

	// To make sure that this framework cleans up after itself, no matter what,
	// we install a Cleanup action before each test and clear it after.  If we
	// should abort, the AfterSuite hook should run all Cleanup actions.
	cleanupHandle CleanupActionHandle

	// configuration for framework's client
	options FrameworkOptions
}

type TestDataSummary interface {
	PrintHumanReadable() string
	PrintJSON() string
}

type FrameworkOptions struct {
	ClientQPS    float32
	ClientBurst  int
	GroupVersion *unversioned.GroupVersion
}

// NewFramework makes a new framework and sets up a BeforeEach/AfterEach for
// you (you can write additional before/after each functions).
func NewDefaultFramework(baseName string) *Framework {
	options := FrameworkOptions{
		ClientQPS:   20,
		ClientBurst: 50,
	}
	return NewFramework(baseName, options, nil)
}

func NewFramework(baseName string, options FrameworkOptions, client clientset.Interface) *Framework {
	f := &Framework{
		BaseName:  baseName,
		options:   options,
		ClientSet: client,
	}

	BeforeEach(f.BeforeEach)
	AfterEach(f.AfterEach)

	return f
}

// BeforeEach gets a client and makes a namespace.
func (f *Framework) BeforeEach() {
	// The fact that we need this feels like a bug in ginkgo.
	// https://github.com/onsi/ginkgo/issues/222
	f.cleanupHandle = AddCleanupAction(f.AfterEach)
	if f.ClientSet == nil {
		By("Creating a kubernetes client")
		config, err := LoadConfig()
		Expect(err).NotTo(HaveOccurred())
		config.QPS = f.options.ClientQPS
		config.Burst = f.options.ClientBurst
		if f.options.GroupVersion != nil {
			config.GroupVersion = f.options.GroupVersion
		}
		if TestContext.KubeAPIContentType != "" {
			config.ContentType = TestContext.KubeAPIContentType
		}
		f.ClientSet, err = clientset.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		f.ClientPool = dynamic.NewClientPool(config, registered.RESTMapper(), dynamic.LegacyAPIPathResolverFunc)
	}

	By("Building a namespace api object")
	namespace, err := f.CreateNamespace(f.BaseName, map[string]string{
		"e2e-framework": f.BaseName,
	})
	Expect(err).NotTo(HaveOccurred())

	f.Namespace = namespace
}

// AfterEach deletes the namespace, after reading its events.
func (f *Framework) AfterEach() {
	RemoveCleanupAction(f.cleanupHandle)

	// DeleteNamespace at the very end in defer, to avoid any
	// expectation failures preventing deleting the namespace.
	defer func() {
		nsDeletionErrors := map[string]error{}
		// Whether to delete namespace is determined by 3 factors: delete-namespace flag, delete-namespace-on-failure flag and the test result
		// if delete-namespace set to false, namespace will always be preserved.
		// if delete-namespace is true and delete-namespace-on-failure is false, namespace will be preserved if test failed.
		if TestContext.DeleteNamespace && (TestContext.DeleteNamespaceOnFailure || !CurrentGinkgoTestDescription().Failed) {
			for _, ns := range f.namespacesToDelete {
				By(fmt.Sprintf("Destroying namespace %q for this suite.", ns.Name))
				timeout := 5 * time.Minute
				if f.NamespaceDeletionTimeout != 0 {
					timeout = f.NamespaceDeletionTimeout
				}
				if err := deleteNS(f.ClientSet, f.ClientPool, ns.Name, timeout); err != nil {
					if !apierrs.IsNotFound(err) {
						nsDeletionErrors[ns.Name] = err
					} else {
						Logf("Namespace %v was already deleted", ns.Name)
					}
				}
			}
		} else {
			if TestContext.DeleteNamespace {
				Logf("Found DeleteNamespace=false, skipping namespace deletion!")
			} else if TestContext.DeleteNamespaceOnFailure {
				Logf("Found DeleteNamespaceOnFailure=false, skipping namespace deletion!")
			}

		}

		// Paranoia-- prevent reuse!
		f.Namespace = nil
		f.ClientSet = nil
		f.namespacesToDelete = nil

		// if we had errors deleting, report them now.
		if len(nsDeletionErrors) != 0 {
			messages := []string{}
			for namespaceKey, namespaceErr := range nsDeletionErrors {
				messages = append(messages, fmt.Sprintf("Couldn't delete ns: %q: %s (%#v)", namespaceKey, namespaceErr, namespaceErr))
			}
			Failf(strings.Join(messages, ","))
		}
	}()

	// Print events if the test failed.
	if CurrentGinkgoTestDescription().Failed && TestContext.DumpLogsOnFailure {
		// Pass both unversioned client and and versioned clientset, till we have removed all uses of the unversioned client.
		DumpAllNamespaceInfo(f.ClientSet, f.Namespace.Name)
	}

	// Check whether all nodes are ready after the test.
	// This is explicitly done at the very end of the test, to avoid
	// e.g. not removing namespace in case of this failure.
	if err := AllNodesReady(f.ClientSet, 3*time.Minute); err != nil {
		Failf("All nodes should be ready after test, %v", err)
	}
}

func (f *Framework) CreateNamespace(baseName string, labels map[string]string) (*v1.Namespace, error) {
	createTestingNS := TestContext.CreateTestingNS
	if createTestingNS == nil {
		createTestingNS = CreateTestingNS
	}
	ns, err := createTestingNS(baseName, f.ClientSet, labels)
	// check ns instead of err to see if it's nil as we may
	// fail to create serviceAccount in it.
	// In this case, we should not forget to delete the namespace.
	if ns != nil {
		f.namespacesToDelete = append(f.namespacesToDelete, ns)
	}
	return ns, err
}

// Wrapper function for ginkgo describe.  Adds namespacing.
// TODO: Support type safe tagging as well https://github.com/kubernetes/kubernetes/pull/22401.
func KubeDescribe(text string, body func()) bool {
	return Describe("[k8s.io] "+text, body)
}
