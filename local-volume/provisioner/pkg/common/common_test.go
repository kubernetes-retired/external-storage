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

package common

import (
	"io/ioutil"
	"os"
	"testing"

	"k8s.io/client-go/rest"
)

func TestSetupClientByKubeConfigEnv(t *testing.T) {
	oldEnv := os.Getenv(KubeConfigEnv)
	os.Setenv(KubeConfigEnv, "/etc/foo/config")
	defer func() { os.Setenv(KubeConfigEnv, oldEnv) }()

	// Mock BuildConfigFromFlags
	oldBuildConfig := BuildConfigFromFlags
	defer func() { BuildConfigFromFlags = oldBuildConfig }()

	methodInvoked := false
	BuildConfigFromFlags = func(masterUrl, kubeconfigPath string) (*rest.Config, error) {
		methodInvoked = true
		if kubeconfigPath != "/etc/foo/config" {
			t.Errorf("Got unexpected oldEnv for config file %s", kubeconfigPath)
		}
		return &rest.Config{}, nil
	}

	SetupClient()
	if !methodInvoked {
		t.Errorf("BuildConfigFromFlags not invoked")
	}
}

func TestSetupClientByInCluster(t *testing.T) {
	// Make sure environment variable is unset
	if oldEnv := os.Getenv(KubeConfigEnv); oldEnv != "" {
		os.Unsetenv(KubeConfigEnv)
		defer func() { os.Setenv(KubeConfigEnv, oldEnv) }()
	}

	// Mock InClusterConfig
	oldInClusterConfig := InClusterConfig
	defer func() { InClusterConfig = oldInClusterConfig }()

	methodInvoked := false
	InClusterConfig = func() (*rest.Config, error) {
		methodInvoked = true
		return &rest.Config{}, nil
	}

	SetupClient()
	if !methodInvoked {
		t.Errorf("InClusterConfig not invoked")
	}
}

func TestLoadProvisionerConfigs(t *testing.T) {
	tmpConfigPath, err := ioutil.TempDir("", "local-provisioner-config")
	if err != nil {
		t.Fatalf("create temp dir error: %v", err)
	}
	defer func() {
		os.RemoveAll(tmpConfigPath)
	}()
	provisionerConfig := &ProvisionerConfiguration{
		StorageClassConfig: make(map[string]StorageClassConfig),
	}
	err = LoadProvisionerConfigs(tmpConfigPath, provisionerConfig)
	if err != nil {
		t.Fatalf("LoadProvisionerConfigs error: %v", err)
	}

	if provisionerConfig.UseAlphaAPI == true {
		t.Errorf("UseAlphaAPI should default to false")
	}
}
